package distill

import "testing"

func TestObservationTemplateParsesAfterBlockExtraction(t *testing.T) {
	s := &server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
}
