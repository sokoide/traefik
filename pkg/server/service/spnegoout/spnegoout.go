package spnegoout

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/containous/alice"
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

// SpnegoOut is a component to make outgoing SPNEGO calls
type SpnegoOut struct {
	next         http.Handler
	config       *dynamic.SpnegoOutService
	client       *client.Client
	spnOverrides map[string]string
}

// New cretes an SpnegoOut service
func New(ctx context.Context, next http.Handler, service *dynamic.SpnegoOutService, name string) (http.Handler, error) {
	logger := log.FromContext(ctx)
	logger.Debug("Creating SpnegoOut service")

	var err error
	// convert array to map
	spnOverrides := make(map[string]string)
	for _, v := range service.SpnOverrides {
		spnOverrides[v.DomainName] = v.Spn
	}

	spnego := &SpnegoOut{
		next:         next,
		config:       service,
		spnOverrides: spnOverrides,
	}

	err = spnego.refreshTicket(logger)
	return spnego, err
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

	// TargetHostSegment is an index of segments when you split URL by '/'.
	// 1st segment is 1.
	// if router's rule is "PathPrefix(`/spnegohttp/`)" and
	// you have a target domain name in the following segment,
	// TargetHostSegment should be set to 2.
	// when you make a request to http://traefikhost:port/spnegohttp/foo.com:12345/a/b/c,
	// it'll redirect the traffic to foo.com:12345 with URL Path = /a/b/c
	// If spnegoOut is defined in loadBalancer service, taergetHostSegment should be 0
	// to skip target host name override.
	if s.config.TargetHostSegment > 0 {
		req.URL.Host = comps[s.config.TargetHostSegment]
		req.URL.Path = "/" + strings.Join(comps[s.config.TargetHostSegment+1:], "/")
	}
	req.Host = req.URL.Host
	req.RequestURI = req.URL.Path

	if value, ok := s.spnOverrides[req.Host]; ok {
		spn = value
	}

	// SetSPNEGOHeader fails if the ticket is expired
	// call refreshTicket() only once if when it fails
	for i := 0; i < 2; i++ {
		err := spnego.SetSPNEGOHeader(s.client, req, spn)
		if err == nil {
			break
		}
		logger.Warnf("Error setting SPNEGO Header. Refreshing ticket. err: %+v", err)
		s.refreshTicket(logger)
	}

	s.next.ServeHTTP(rw, req)
}

// WrapServiceHandler Wraps metrics service to alice.Constructor.
func WrapServiceHandler(ctx context.Context, service *dynamic.SpnegoOutService, name string) alice.Constructor {
	return func(next http.Handler) (http.Handler, error) {
		return New(ctx, next, service, name)
	}
}

func (s *SpnegoOut) refreshTicket(logger log.Logger) error {
	var err error
	var kt *keytab.Keytab
	var ccache *credentials.CCache
	var c *config.Config
	var cl *client.Client
	var krb5ConfReader *os.File

	// read krb5.conf
	var krbConfPath string
	if s.config.KrbConfPath != "" {
		krbConfPath = s.config.KrbConfPath
	} else {
		krbConfPath = "/etc/krb5.conf"
	}

	krb5ConfReader, err = os.Open(krbConfPath)
	if err != nil {
		return err
	}
	defer krb5ConfReader.Close()

	c, err = config.NewFromReader(krb5ConfReader)
	if err != nil {
		return err
	}
	c.LibDefaults.NoAddresses = true

	if s.config.KeytabPath != "" {
		logger.Debugf("Using Keytab %s", s.config.KeytabPath)
		user := s.config.User
		if user == "" {
			user = os.Getenv("USER")
		}
		realm := s.config.Realm
		if realm == "" {
			realm = c.LibDefaults.DefaultRealm
		}
		kt, err = keytab.Load(s.config.KeytabPath)
		if err != nil {
			return err
		}
		cl = client.NewWithKeytab(user, realm, kt, c)
	} else if s.config.CcachePath != "" {
		logger.Debugf("Using Ccache %s", s.config.CcachePath)
		ccache, err = credentials.LoadCCache(s.config.CcachePath)
		if err != nil {
			return err
		}
		cl, err = client.NewFromCCache(ccache, c)
		if err != nil {
			return err
		}
	} else {
		msg := "Either KeytabPath or CcachePath must be specified"
		logger.Error(msg)
		return errors.New(msg)
	}

	s.client = cl
	return nil
}
