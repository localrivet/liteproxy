package compose

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
)

const (
	LabelHost         = "liteproxy.host"
	LabelPort         = "liteproxy.port"
	LabelPath         = "liteproxy.path"
	LabelRedirectFrom = "liteproxy.redirect_from"
	LabelPassHost     = "liteproxy.passhost"
	LabelStripPrefix  = "liteproxy.strip_prefix"
)

// Route represents a single routing rule extracted from compose labels
type Route struct {
	Host           string
	PathPrefix     string
	ServiceName    string
	ServicePort    int
	PassHostHeader bool
	StripPrefix    bool
	RedirectFrom   []string
}

// ParseFile reads a compose file and extracts routes from labeled services
func ParseFile(path string) ([]Route, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading compose file: %w", err)
	}

	return Parse(data, path)
}

// Parse parses compose yaml data and extracts routes from labeled services
func Parse(data []byte, filename string) ([]Route, error) {
	config := types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{
			{
				Filename: filename,
				Content:  data,
			},
		},
	}

	project, err := loader.LoadWithContext(context.Background(), config, func(options *loader.Options) {
		options.SkipInterpolation = true
	}, loader.WithSkipValidation)
	if err != nil {
		return nil, fmt.Errorf("parsing compose file: %w", err)
	}

	var routes []Route
	for _, service := range project.Services {
		route, err := extractRoute(service)
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", service.Name, err)
		}
		if route != nil {
			routes = append(routes, *route)
		}
	}

	return routes, nil
}

// extractRoute extracts a Route from service labels, returns nil if no liteproxy labels
func extractRoute(service types.ServiceConfig) (*Route, error) {
	labels := service.Labels

	host := labels[LabelHost]
	portStr := labels[LabelPort]

	// No liteproxy labels = not proxied
	if host == "" && portStr == "" {
		return nil, nil
	}

	// If one is set, both are required
	if host == "" {
		return nil, fmt.Errorf("missing required label %s", LabelHost)
	}
	if portStr == "" {
		return nil, fmt.Errorf("missing required label %s", LabelPort)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port %q: %w", portStr, err)
	}

	route := &Route{
		Host:        host,
		ServiceName: service.Name,
		ServicePort: port,
		PathPrefix:  "/",
		StripPrefix: true, // default to stripping
	}

	// Optional: path prefix
	if path := labels[LabelPath]; path != "" {
		route.PathPrefix = path
	}

	// Optional: passhost
	if passhost := labels[LabelPassHost]; passhost != "" {
		route.PassHostHeader = passhost == "true"
	}

	// Optional: strip_prefix (defaults to true)
	if stripPrefix := labels[LabelStripPrefix]; stripPrefix != "" {
		route.StripPrefix = stripPrefix != "false"
	}

	// Optional: redirect_from (comma-separated)
	if redirectFrom := labels[LabelRedirectFrom]; redirectFrom != "" {
		domains := strings.Split(redirectFrom, ",")
		for i, d := range domains {
			domains[i] = strings.TrimSpace(d)
		}
		route.RedirectFrom = domains
	}

	return route, nil
}
