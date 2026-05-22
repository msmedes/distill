package distill

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObservationTemplateParsesAfterBlockExtraction(t *testing.T) {
	s := &server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
}

func TestHelpPageRenders(t *testing.T) {
	s := &server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/help", nil)
	rr := httptest.NewRecorder()
	s.handleHelp(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"promotion destinations", "What does always-on mean?", "What is a skill?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("help page missing %q", want)
		}
	}
}
