package spnegoout

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/keytab"
	"github.com/jcmturner/gokrb5/v8/spnego"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/log"
)

const (
	typeName = "SpnegoOut"
)

// SpnegoOUt is a component to make outgoing SPNEGO calls
type SpnegoOut struct {
	next         http.Handler
	config       *dynamic.SpnegoOutService
	client       *client.Client
	spnOverrides map[string]string
}

// New cretes an SpnegoOUt service
func New(ctx context.Context, next http.Handler, service *dynamic.SpnegoOutService, name string) (http.Handler, error) {
	logger := log.FromContext(ctx)
	logger.Debug("Creating SpnegoOut service")

	var err error
	var kt *keytab.Keytab
	var ccache *credentials.CCache
	var c *config.Config
	var cl *client.Client
	var krb5ConfReader *os.File

	// read krb5.conf
	var krbConfPath string
	if service.KrbConfPath != "" {
		krbConfPath = service.KrbConfPath
	} else {
		krbConfPath = "/etc/krb5.conf"
	}

	krb5ConfReader, err = os.Open(krbConfPath)
	if err != nil {
		return nil, err
	}
	defer krb5ConfReader.Close()

	c, err = config.NewFromReader(krb5ConfReader)
	if err != nil {
		return nil, err
	}
	c.LibDefaults.NoAddresses = true

	if service.KeytabPath != "" {
		logger.Debugf("Using Keytab %s", service.KeytabPath)
		user := fmt.Sprintf("%s/%s", os.Getenv("USER"), os.Getenv("HOSTNAME"))
		realm := service.Realm
		kt, err = keytab.Load(service.KeytabPath)
		if err != nil {
			return nil, err
		}
		cl = client.NewWithKeytab(user, realm, kt, c)
	} else if service.CcachePath != "" {
		logger.Debugf("Using Ccache %s", service.CcachePath)
		ccache, err = credentials.LoadCCache(service.CcachePath)
		if err != nil {
			return nil, err
		}
		cl, err = client.NewFromCCache(ccache, c)
		if err != nil {
			return nil, err
		}
	} else {
		msg := "Either KeytabPath or CcachePath must be specified"
		logger.Error(msg)
		return nil, errors.New(msg)
	}

	// convert array to map
	spnOverrides := make(map[string]string)
	for _, v := range service.SpnOverrides {
		spnOverrides[v.DomainName] = v.Spn
	}

	spnego := &SpnegoOut{
		next:         next,
		config:       service,
		client:       cl,
		spnOverrides: spnOverrides,
	}

	return spnego, nil
}

func (s *SpnegoOut) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	logger := log.FromContext(req.Context())
	spn := ""

	path := req.URL.Path
	if len(path) > 0 && path[0] != '/' {
		path = "/" + path
	}
	comps := strings.Split(req.URL.Path, "/")

	if len(s.config.Scheme) > 0 {
		req.URL.Scheme = s.config.Scheme
	} else {
		req.URL.Scheme = "HTTP"
	}

	// if router's rule is "PathPrefix(`/spnegohttp/`)"
	// TargetHostSegment must be set to 1.
	// when you make a request to http://traefikhost:port/spnegohttp/foo.com:12345/a/b/c,
	// it'll redirect the traffic to foo.com:12345 with URL Path = /a/b/c
	req.URL.Host = comps[s.config.TargetHostSegment+1]
	req.URL.Path = "/" + strings.Join(comps[s.config.TargetHostSegment+2:], "/")
	req.Host = req.URL.Host
	req.RequestURI = req.URL.Path

	if value, ok := s.spnOverrides[req.Host]; ok {
		spn = value
	}

	err := spnego.SetSPNEGOHeader(s.client, req, spn)
	if err != nil {
		logger.Errorf("Error setting SPNEGO Header %v", err)
	}

	logger.Debugf("req: %+v", req)
	s.next.ServeHTTP(rw, req)
}
