package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pinnable is a pointer so a probed "false" survives JSON instead of being
// dropped by omitempty. The console keys its disabled pin controls off that
// explicit false value.
func TestModelMetaPinnableJSON(t *testing.T) {
	raw, err := json.Marshal(ModelMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pinnable") {
		t.Fatalf("an unprobed model should omit pinnable: %s", raw)
	}

	value := false
	raw, err = json.Marshal(ModelMeta{Pinnable: &value})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"pinnable":false`) {
		t.Fatalf("pinnable=false must be serialized: %s", raw)
	}

	value = true
	raw, err = json.Marshal(ModelMeta{Pinnable: &value})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"pinnable":true`) {
		t.Fatalf("pinnable=true must be serialized: %s", raw)
	}
}
