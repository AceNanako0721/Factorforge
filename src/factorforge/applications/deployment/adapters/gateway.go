package adapters

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func Gateway(ctx context.Context, cert, key string) error {
	routes := map[int]string{8443: "http://trading-api:8000", 9443: "http://strategy-api:8001", 9444: "http://strategy-internal:8002", 10443: "http://soxl-jev-api:8003"}
	servers := []*http.Server{}
	done := make(chan error, len(routes))
	for port, target := range routes {
		u, _ := url.Parse(target)
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			http.Error(w, "UPSTREAM_UNAVAILABLE", http.StatusBadGateway)
		}
		srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: proxy, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
		servers = append(servers, srv)
		go func() { done <- srv.ListenAndServeTLS(cert, key) }()
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, s := range servers {
			_ = s.Shutdown(c)
		}
	}()
	select {
	case <-ctx.Done():
		return nil
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return StableError("TLS_GATEWAY_FAILED")
	}
}
