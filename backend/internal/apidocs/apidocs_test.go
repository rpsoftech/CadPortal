// Package apidocs holds the test that keeps docs/api/openapi.yaml in step with the routes the API
// really registers. It has no code of its own.
package apidocs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"

	"github.com/rpsoftech/cad-portal/backend/internal/handlers"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

var (
	ginParam  = regexp.MustCompile(`[:*][A-Za-z_][A-Za-z0-9_]*`)
	specParam = regexp.MustCompile(`\{[^}]+\}`)
	methods   = map[string]bool{"get": true, "post": true, "put": true, "delete": true, "patch": true}
)

type spec struct {
	Tags []struct {
		Name string `yaml:"name"`
	} `yaml:"tags"`
	Paths map[string]map[string]operation `yaml:"paths"`
}

type operation struct {
	Summary   string         `yaml:"summary"`
	Tags      []string       `yaml:"tags"`
	Responses map[string]any `yaml:"responses"`
}

func loadSpec(t *testing.T) spec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read the API spec: %v", err)
	}
	var s spec
	if err := yaml.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse the API spec: %v", err)
	}
	return s
}

// registeredRoutes asks the real router which routes it serves. Parameter names do not matter to a
// client, so /designs/:code and /designs/:id both read "/designs/{}".
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	local, err := storage.NewLocal(t.TempDir(), []byte("0123456789abcdef0123456789abcdef0123"))
	if err != nil {
		t.Fatalf("local store: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&handlers.H{JWTSecret: []byte("0123456789abcdef0123456789abcdef0123"), LocalFiles: local}).Register(r)

	got := map[string]bool{}
	for _, ri := range r.Routes() {
		got[strings.ToLower(ri.Method)+" "+ginParam.ReplaceAllString(ri.Path, "{}")] = true
	}
	return got
}

func documentedRoutes(s spec) map[string]bool {
	got := map[string]bool{}
	for path, ops := range s.Paths {
		for method := range ops {
			if methods[method] {
				got[method+" "+specParam.ReplaceAllString(path, "{}")] = true
			}
		}
	}
	return got
}

func missing(have, want map[string]bool) []string {
	var out []string
	for k := range want {
		if !have[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// A route added or removed without touching docs/api/openapi.yaml fails here. After editing the spec run
// `python3 docs/api/gen_postman.py` to refresh the Postman collection (CI checks that too).
func TestSpecDocumentsEveryRoute(t *testing.T) {
	routes, docs := registeredRoutes(t), documentedRoutes(loadSpec(t))

	if m := missing(docs, routes); len(m) > 0 {
		t.Errorf("routes the API serves but docs/api/openapi.yaml does not describe:\n  %s", strings.Join(m, "\n  "))
	}
	if m := missing(routes, docs); len(m) > 0 {
		t.Errorf("operations in docs/api/openapi.yaml that the API does not serve:\n  %s", strings.Join(m, "\n  "))
	}
}

// Every operation needs the few things the Postman folders and the generated pages are built from.
func TestSpecOperationsAreComplete(t *testing.T) {
	s := loadSpec(t)
	declared := map[string]bool{}
	for _, tag := range s.Tags {
		declared[tag.Name] = true
	}
	for path, ops := range s.Paths {
		for method, op := range ops {
			if !methods[method] {
				continue
			}
			id := strings.ToUpper(method) + " " + path
			if strings.TrimSpace(op.Summary) == "" {
				t.Errorf("%s has no summary", id)
			}
			if len(op.Tags) == 0 {
				t.Errorf("%s has no tag", id)
			}
			for _, tag := range op.Tags {
				if !declared[tag] {
					t.Errorf("%s uses the tag %q, which is not declared under tags:", id, tag)
				}
			}
			if len(op.Responses) == 0 {
				t.Errorf("%s documents no responses", id)
			}
		}
	}
}
