package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestVersionShownInTitleAndUI(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(version) {
		t.Fatalf("version %q is not semver-like", version)
	}
	if appTitle() != "Enkoder "+version {
		t.Fatalf("unexpected title %q", appTitle())
	}
	html := uiHTML()
	if strings.Contains(html, "{{VERSION}}") {
		t.Fatal("UI still contains an unfilled {{VERSION}} placeholder")
	}
	if !strings.Contains(html, "<title>Enkoder "+version+"</title>") {
		t.Fatal("UI <title> must carry the version")
	}
}

func TestJobToOptionsAV1AndHDR10Validation(t *testing.T) {
	a := &app{} // no ffmpeg: AV1 must be rejected as unavailable
	cq := 15
	job := jobDTO{Input: "in.mkv", Output: "out.mkv", Video: []int{0}, Codec: "libsvtav1", CQ: &cq}
	if _, err := a.jobToOptions(job); err == nil {
		t.Fatal("AV1 must be rejected when libsvtav1 is unavailable")
	}

	job.Codec = "libx265"
	job.MasterDisplay = "G(1,2)B(3,4)R(5,6)WP(7,8)L(9,10):pools=1"
	if _, err := a.jobToOptions(job); err == nil {
		t.Fatal("master-display with extra x265 params must be rejected")
	}
	job.MasterDisplay = "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"
	job.MaxCLL = "1000,400:crf=0"
	if _, err := a.jobToOptions(job); err == nil {
		t.Fatal("max-cll with extra x265 params must be rejected")
	}
	job.MaxCLL = "1000,400"
	opt, err := a.jobToOptions(job)
	if err != nil {
		t.Fatal(err)
	}
	if opt.MasterDisplay != job.MasterDisplay || opt.MaxCLL != "1000,400" {
		t.Fatalf("HDR10 metadata not carried: %+v", opt)
	}

	big := 60
	job.CQ = &big
	if _, err := a.jobToOptions(job); err == nil {
		t.Fatal("x265 CRF above 51 must be rejected")
	}
}
