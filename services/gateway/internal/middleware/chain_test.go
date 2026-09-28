package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func recording(name string, order *[]string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*order = append(*order, name+":in")
			next.ServeHTTP(w, r)
			*order = append(*order, name+":out")
		})
	}
}

func TestChain_OrdineDiEsecuzione(t *testing.T) {
	var order []string

	chain := Chain(
		recording("outer", &order),
		recording("middle", &order),
		recording("inner", &order),
	)
	handler := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{
		"outer:in", "middle:in", "inner:in",
		"handler",
		"inner:out", "middle:out", "outer:out",
	}
	if len(order) != len(want) {
		t.Fatalf("ordine = %v, voluto %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ordine = %v, voluto %v", order, want)
		}
	}
}

func TestChain_Vuota(t *testing.T) {
	chain := Chain()
	called := false
	handler := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Error("una catena vuota deve comunque chiamare il gestore finale")
	}
}
