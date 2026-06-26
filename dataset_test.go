package evalgo

import (
	"strings"
	"testing"
)

func TestLoadJSONL(t *testing.T) {
	in := `{"name":"a","input":"q1","output":"o1"}

{"name":"b","input":"q2","output":"o2"}
`
	samples, err := LoadJSONL(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].Name != "a" || samples[1].Output != "o2" {
		t.Fatalf("unexpected: %+v", samples)
	}
}

func TestLoadCSV(t *testing.T) {
	in := "name,input,output,context,expected_tools,attack\n" +
		"s1,what rate?,0.3%,chunkA|chunkB,search|calc,none\n" +
		"s2,jailbreak,I can't,,,jailbreak\n"
	samples, err := LoadCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("want 2, got %d", len(samples))
	}
	if len(samples[0].Context) != 2 || samples[0].Context[1] != "chunkB" {
		t.Errorf("context split wrong: %+v", samples[0].Context)
	}
	// expected_tools splits on ',' so "search|calc" is one token here (no comma)
	if samples[0].Meta["attack"] != "none" {
		t.Errorf("meta column not captured: %+v", samples[0].Meta)
	}
	if samples[1].Meta["attack"] != "jailbreak" {
		t.Errorf("row 2 attack meta wrong: %+v", samples[1].Meta)
	}
}

func TestFilterMeta(t *testing.T) {
	samples := []Sample{
		{Name: "a", Meta: map[string]string{"attack": "jailbreak"}},
		{Name: "b", Meta: map[string]string{"attack": "pii_extraction"}},
		{Name: "c", Meta: map[string]string{"attack": "jailbreak"}},
	}
	got := FilterMeta(samples, "attack", "jailbreak")
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Errorf("filter wrong: %+v", got)
	}
}
