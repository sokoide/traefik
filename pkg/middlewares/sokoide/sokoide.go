package sokoide

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/middlewares"
)

const typeName = "Sokoide"

type sokoide struct {
	next    http.Handler
	name    string
	enabled bool
}

// New creates a new replace path middleware.
func New(ctx context.Context, next http.Handler, config dynamic.Sokoide, name string) (http.Handler, error) {
	middlewares.GetLogger(ctx, name, typeName).Debug().Msg("Creating middleware")

	return &sokoide{
		next:    next,
		name:    name,
		enabled: config.Enabled,
	}, nil
}

func (r *sokoide) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	ctx := context.Background()
	logger := middlewares.GetLogger(ctx, r.name, typeName)

	currentPath := req.URL.RawPath
	if currentPath == "" {
		currentPath = req.URL.EscapedPath()
	}

	logger.Info().Msg("Sokoide: ServerHTTP")

	if r.enabled {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			logger.Error().Msgf("err: %v", err)
			return
		}

		// restore body
		req.Body = io.NopCloser(bytes.NewBuffer(body))
		r.next.ServeHTTP(rw, req)
	} else {
		r.next.ServeHTTP(rw, req)
	}
}
