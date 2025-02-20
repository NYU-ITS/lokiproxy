package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/nyu-its/lokiproxy/internal/config"
	"github.com/nyu-its/lokiproxy/internal/parser"
)

// GET /loki/api/v1/query
//   query params to change: query
//   query params to pass: limit, time, direction
// GET /loki/api/v1/query_range
//   query params to change: query
//   query params to pass: limit, start, end, since, step, interval, direction
// GET /loki/api/v1/series
//   query params to change: match[]
//   query params to pass: start, end, since
//   can be POST form-urlencoded
// GET /loki/api/v1/tail
//   query params to change: query
//   query params to pass: delay_for, limit, start
// GET /loki/api/v1/labels
//   query params to pass: start, end, since
// GET /loki/api/v1/label/<name>/values
//   query params to change/add: query
//	 query params to pass: start, end, since
// GET /loki/api/v1/index/stats
//   query params to change: query
//   query params to pass: start, end
//   can be POST form-urlencoded

var proxyClient *http.Client
var lokiUrl url.URL
var oidcVerifier *oidc.IDTokenVerifier
var identityMap *config.FileMultiMap
var allowAlerts bool = false

func main() {
	ctx, cancelCtx := context.WithCancel(context.Background())

	proxyClient = &http.Client{}

	// Read upstream URL
	{
		arg := os.Getenv("LOKIPROXY_UPSTREAM_URL")
		if arg == "" {
			log.Fatalf("LOKIPROXY_UPSTREAM_URL is not set")
		}
		r, err := url.Parse(arg)
		if err != nil {
			log.Fatalf("can't parse Loki upstream URL: %s", err)
		}
		lokiUrl = *r
	}
	log.Printf("upstream scheme: %s", lokiUrl.Scheme)

	// Read upstream TLS configuration
	{
		var rootCAs *x509.CertPool
		argCA := os.Getenv("LOKIPROXY_UPSTREAM_CA")
		if argCA != "" {
			pem, err := os.ReadFile(argCA)
			if err != nil {
				log.Fatalf("can't open upstream CA certificate bundle: %s", err)
			}
			rootCAs = x509.NewCertPool()
			rootCAs.AppendCertsFromPEM(pem)
			log.Printf("upstream custom CA loaded")
		}

		var certificates []tls.Certificate
		argCert := os.Getenv("LOKIPROXY_UPSTREAM_CERT")
		argKey := os.Getenv("LOKIPROXY_UPSTREAM_KEY")
		if argCert != "" || argKey != "" {
			cert, err := tls.LoadX509KeyPair(argCert, argKey)
			if err != nil {
				log.Fatalf("can't load client certificate: %s", err)
			}
			certificates = []tls.Certificate{cert}
			log.Printf("upstream client certificate loaded")
		}

		proxyClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: certificates,
				RootCAs:      rootCAs,
			},
		}
	}

	// Read frontend TLS configuration
	var serverTLSConfig *tls.Config
	var serverCertificate *[2]string
	{
		argCert := os.Getenv("LOKIPROXY_FRONTEND_CERT")
		argKey := os.Getenv("LOKIPROXY_FRONTEND_KEY")
		argCA := os.Getenv("LOKIPROXY_FRONTEND_CA")
		if argCert != "" || argKey != "" {
			serverCertificate = &[2]string{argCert, argKey}
			log.Printf("frontend certificate set")

			var rootCAs *x509.CertPool
			if argCA != "" {
				pem, err := os.ReadFile(argCA)
				if err != nil {
					log.Fatalf("can't open frontend CA certificate bundle: %s", err)
				}
				rootCAs = x509.NewCertPool()
				rootCAs.AppendCertsFromPEM(pem)
				serverTLSConfig = &tls.Config{
					ClientCAs: rootCAs,
					ClientAuth: tls.RequireAndVerifyClientCert,
				}
				log.Printf("frontend CA loaded")
				log.Printf("frontend using mTLS")
			} else {
				log.Printf("frontend using TLS")
			}
		} else {
			if argCA != "" {
				log.Fatalf("can't use frontend CA without a frontend certificate")
			}
			log.Printf("frontend not using TLS")
		}
	}

	// Create OIDC verifier
	{
		argProvider := os.Getenv("LOKIPROXY_OIDC_PROVIDER")
		if argProvider == "" {
			log.Fatalf("LOKIPROXY_OIDC_PROVIDER is not set")
		}
		argClientID := os.Getenv("LOKIPROXY_OIDC_CLIENT_ID")
		if argClientID == "" {
			log.Fatalf("LOKIPROXY_OIDC_CLIENT_ID is not set")
		}
		provider, err := oidc.NewProvider(ctx, argProvider)
		if err != nil {
			log.Fatalf("can't create OIDC provider: %s", err)
		}
		oidcVerifier = provider.Verifier(&oidc.Config{ClientID: argClientID})
	}

	// Read list of user namespaces
	{
		arg := os.Getenv("LOKIPROXY_IDENTITY_MAP_FILE")
		if arg == "" {
			log.Fatalf("LOKIPROXY_IDENTITY_MAP_FILE is not set")
		}
		var err error
		identityMap, err = config.NewFileMultiMap(arg, cancelCtx)
		if err != nil {
			log.Fatalf("error loading identity map: %s", err)
		}
	}

	// Read listen address
	listenAddr := os.Getenv("LOKIPROXY_LISTEN_ADDR")
	if listenAddr == "" {
		log.Fatalf("LOKIPROXY_LISTEN_ADDR is not set")
	}

	// Read whether to allow alerts (which don't come with an ID Token)
	{
		arg := os.Getenv("LOKIPROXY_ALLOW_ALERTS")
		if arg == "" || arg == "0" || arg == "false" || arg == "no" {
			allowAlerts = false
		} else if arg == "1" || arg == "true" || arg == "yes" {
			allowAlerts = true
		} else {
			log.Fatalf("unrecognized value for LOKIPROXY_ALLOW_ALERTS: %#v", arg)
		}
	}

	// Set up routes
	mux := http.NewServeMux()
	mux.HandleFunc("/", handle404)
	mux.HandleFunc("/loki/api/v1/query", handleQuery)
	mux.HandleFunc("/loki/api/v1/query_range", handleQueryRange)
	mux.HandleFunc("/loki/api/v1/series", handleSeries)
	mux.HandleFunc("/loki/api/v1/tail", handleTail)
	mux.HandleFunc("/loki/api/v1/labels", handleLabels)
	mux.HandleFunc("/loki/api/v1/label/{label}/values", handleLabelValues)
	mux.HandleFunc("/loki/api/v1/index/stats", handleIndexStats)

	// Create HTTP server
	server := http.Server{
		Addr:    listenAddr,
		Handler: mux,
		TLSConfig: serverTLSConfig,
	}
	context.AfterFunc(ctx, func() { server.Close() })
	log.Printf("Listening on %s", listenAddr)
	var err error
	if serverCertificate == nil {
		err = server.ListenAndServe()
	} else {
		err = server.ListenAndServeTLS(serverCertificate[0], serverCertificate[1])
	}
	if !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func makeProxyUrl(path string) url.URL {
	r := lokiUrl
	r.Path = path
	return r
}

func getRequiredLabelsForUser(res http.ResponseWriter, req *http.Request) (map[string]interface{}, bool) {
	// Get user identity
	idTokens := req.Header["X-Id-Token"]
	if idTokens == nil || len(idTokens) != 1 {
		if idTokens == nil && allowAlerts {
			alertHeaders := req.Header["Fromalert"]
			if len(alertHeaders) == 1 && alertHeaders[0] == "true" {
				log.Print("allowing alert")
				return make(map[string]interface{}), true
			}
		}
		http.Error(res, "missing ID token", 410)
		return nil, false
	}
	idToken, err := oidcVerifier.Verify(req.Context(), idTokens[0])
	if err != nil {
		log.Print("invalid ID token")
		http.Error(res, "invalid ID token", 410)
		return nil, false
	}
	log.Printf("id token: %#v", idToken.Subject)

	// Get required labels for user
	requiredLabels, ok := identityMap.Get(idToken.Subject)
	if !ok {
		log.Printf("request from unknown user %s", idToken.Subject)
		http.Error(res, "unknown user", 403)
		return nil, false
	}

	return requiredLabels, true
}

func respondWithProxy(
	path string,
	args url.Values,
	res http.ResponseWriter,
	setArgs map[string][]string,
	passthroughArgs []string,
	ctx context.Context,
) {
	// Assemble query parameters
	proxyQuery := make(url.Values)
	for _, key := range passthroughArgs {
		values, ok := args[key]
		if ok {
			proxyQuery[key] = values
		}
	}
	for key, values := range setArgs {
		proxyQuery[key] = values
	}

	// Do request
	proxyUrl := makeProxyUrl(path)
	proxyUrl.RawQuery = proxyQuery.Encode()
	proxyReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		proxyUrl.String(),
		nil,
	)
	if err != nil {
		log.Printf("error creating proxy request: %s", err)
		http.Error(res, "internal error", 500)
		return
	}
	proxyRes, err := proxyClient.Do(proxyReq)
	if err != nil {
		log.Printf("error sending proxy request: %s", err)
		http.Error(res, "internal error", 503)
		return
	}

	// Send response
	res.Header()["Content-Type"] = proxyRes.Header["Content-Type"]
	res.WriteHeader(proxyRes.StatusCode)
	_, _ = io.Copy(res, proxyRes.Body)
}

func handle404(res http.ResponseWriter, req *http.Request) {
	log.Printf("404: %s %s", req.Method, req.URL.Path)
	http.Error(res, "Not found", 404)
}

func handleQuery(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		if err := req.ParseForm(); err != nil {
			http.Error(res, "invalid form data", 400)
			return
		}

		if len(req.Form["query"]) != 1 {
			http.Error(res, "one query expected", 400)
			return
		}
		query := req.Form["query"][0]

		// Find user, get required labels
		requiredLabels, ok := getRequiredLabelsForUser(res, req)
		if !ok {
			return
		}

		// Rewrite query
		query, err := parser.ProcessQuery(query, requiredLabels)
		if err != nil {
			http.Error(res, fmt.Sprintf("error parsing query: %s", err), 400)
			return
		}

		// Proxy
		respondWithProxy(
			"/loki/api/v1/query",
			req.Form,
			res,
			map[string][]string{"query": []string{query}},
			[]string{"limit", "time", "direction"},
			req.Context(),
		)
	} else {
		log.Printf("got %s to query", req.Method)
		http.Error(res, "use method GET", 400)
		return
	}
}

func handleQueryRange(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		if err := req.ParseForm(); err != nil {
			http.Error(res, "invalid form data", 400)
			return
		}

		if len(req.Form["query"]) != 1 {
			http.Error(res, "one query expected", 400)
			return
		}
		query := req.Form["query"][0]

		// Find user, get allowed namespaces
		requiredLabels, ok := getRequiredLabelsForUser(res, req)
		if !ok {
			return
		}

		// Rewrite query
		query, err := parser.ProcessQuery(query, requiredLabels)
		if err != nil {
			http.Error(res, fmt.Sprintf("error parsing query: %s", err), 400)
			return
		}

		// Proxy
		respondWithProxy(
			"/loki/api/v1/query_range",
			req.Form,
			res,
			map[string][]string{"query": []string{query}},
			[]string{"limit", "start", "end", "since", "step", "interval", "direction"},
			req.Context(),
		)
	} else {
		log.Printf("got %s to query_range", req.Method)
		http.Error(res, "use method GET", 400)
		return
	}
}

func handleSeries(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" || req.Method == "POST" {
		if err := req.ParseForm(); err != nil {
			http.Error(res, "invalid form data", 400)
			return
		}

		queries := req.Form["match[]"]

		// Find user, get allowed namespaces
		requiredLabels, ok := getRequiredLabelsForUser(res, req)
		if !ok {
			return
		}

		// Rewrite query
		for key := range queries {
			var err error
			queries[key], err = parser.ProcessQuery(queries[key], requiredLabels)
			if err != nil {
				http.Error(res, fmt.Sprintf("error parsing query: %s", err), 400)
				return
			}
		}

		// Proxy
		respondWithProxy(
			"/loki/api/v1/series",
			req.Form,
			res,
			map[string][]string{"query": queries},
			[]string{"limit", "start", "end", "since", "step", "interval", "direction"},
			req.Context(),
		)
	} else {
		log.Printf("got %s to series", req.Method)
		http.Error(res, "use methods GET or POST", 400)
		return
	}
}

func handleTail(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		if err := req.ParseForm(); err != nil {
			http.Error(res, "invalid form data", 400)
			return
		}

		if len(req.Form["query"]) != 1 {
			http.Error(res, "one query expected", 400)
			return
		}
		query := req.Form["query"][0]

		// Find user, get allowed namespaces
		requiredLabels, ok := getRequiredLabelsForUser(res, req)
		if !ok {
			return
		}

		// Rewrite query
		query, err := parser.ProcessQuery(query, requiredLabels)
		if err != nil {
			http.Error(res, fmt.Sprintf("error parsing query: %s", err), 400)
			return
		}

		// Proxy
		respondWithProxy(
			"/loki/api/v1/tail",
			req.Form,
			res,
			map[string][]string{"query": []string{query}},
			[]string{"delay_for", "limit", "start"},
			req.Context(),
		)
	} else {
		log.Printf("got %s to tail", req.Method)
		http.Error(res, "use method GET", 400)
		return
	}
}

func handleLabels(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		if err := req.ParseForm(); err != nil {
			http.Error(res, "invalid form data", 400)
			return
		}

		// Proxy
		respondWithProxy(
			"/loki/api/v1/labels",
			req.Form,
			res,
			map[string][]string{},
			[]string{"start", "end", "since"},
			req.Context(),
		)
	} else {
		log.Printf("got %s to labels", req.Method)
		http.Error(res, "use method GET", 400)
		return
	}
}

func handleLabelValues(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		if err := req.ParseForm(); err != nil {
			http.Error(res, "invalid form data", 400)
			return
		}

		var query string
		if len(req.Form["query"]) == 1 {
			query = req.Form["query"][0]
		} else if len(req.Form["query"]) == 0 {
			// No query is acceptable, treat as {} if we need to add labels
		} else {
			http.Error(res, "only one query expected", 400)
			return
		}

		// Find user, get required labels
		requiredLabels, ok := getRequiredLabelsForUser(res, req)
		if !ok {
			return
		}

		// Rewrite query
		if len(query) > 0 || len(requiredLabels) > 0 {
			if len(query) == 0 {
				query = "{}"
			}
			var err error
			query, err = parser.ProcessQuery(query, requiredLabels)
			if err != nil {
				http.Error(res, fmt.Sprintf("error parsing query: %s", err), 400)
				return
			}
		}

		// Proxy
		respondWithProxy(
			fmt.Sprintf("/loki/api/v1/label/%s/values", req.PathValue("label")),
			req.Form,
			res,
			map[string][]string{"query": []string{query}},
			[]string{"start", "end"},
			req.Context(),
		)
	} else {
		log.Printf("got %s to label values", req.Method)
		http.Error(res, "use method GET", 400)
		return
	}
}

func handleIndexStats(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" || req.Method == "POST" {
		if err := req.ParseForm(); err != nil {
			http.Error(res, "invalid form data", 400)
			return
		}

		if len(req.Form["query"]) != 1 {
			http.Error(res, "one query expected", 400)
			return
		}
		query := req.Form["query"][0]

		// Find user, get allowed namespaces
		requiredLabels, ok := getRequiredLabelsForUser(res, req)
		if !ok {
			return
		}

		// Rewrite query
		query, err := parser.ProcessQuery(query, requiredLabels)
		if err != nil {
			http.Error(res, fmt.Sprintf("error parsing query: %s", err), 400)
			return
		}

		// Proxy
		respondWithProxy(
			"/loki/api/v1/index/stats",
			req.Form,
			res,
			map[string][]string{"query": []string{query}},
			[]string{"start", "end"},
			req.Context(),
		)
	} else {
		log.Printf("got %s to index/stats", req.Method)
		http.Error(res, "use methods GET or POST", 400)
		return
	}
}
