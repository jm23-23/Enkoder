package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"enkoder/internal/ffmpeg"
)

func TestSameFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "film.mkv")
	other := filepath.Join(dir, "other.mkv")
	for _, p := range []string{src, other} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link.mkv")
	if err := os.Symlink(src, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	hard := filepath.Join(dir, "hard.mkv")
	if err := os.Link(src, hard); err != nil {
		t.Skip("no hard links:", err)
	}
	cases := []struct {
		out  string
		want bool
	}{
		{src, true},
		{filepath.Join(dir, ".", "film.mkv"), true},
		{" " + src + " ", true},
		{link, true},
		{hard, true},
		{other, false},
		{filepath.Join(dir, "missing.mkv"), false},
		{"", false},
	}
	for _, c := range cases {
		if got := sameFile(src, c.out); got != c.want {
			t.Errorf("sameFile(src, %q) = %v, want %v", c.out, got, c.want)
		}
	}
}

func TestJobRejectsOutputEqualToSource(t *testing.T) {
	defer setLang(lang())
	setLang(langEN)
	src := filepath.Join(t.TempDir(), "film.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{}
	job := jobDTO{Input: src, Output: src, Video: []int{0}, Codec: "copy", Container: "mkv"}
	if _, err := a.jobToOptions(job); err == nil || !strings.Contains(err.Error(), "source file") {
		t.Fatalf("same file: err = %v", err)
	}
	job.Output = filepath.Join(filepath.Dir(src), "film.x265.mkv")
	if _, err := a.jobToOptions(job); err != nil {
		t.Fatalf("different file: %v", err)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mkv")
	if fileExists(p) {
		t.Fatal("missing file reported as existing")
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(p) {
		t.Fatal("existing file not found")
	}
	// A dangling symlink still blocks the name; ffmpeg would write through it.
	dangling := filepath.Join(dir, "dangling.mkv")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err == nil && !fileExists(dangling) {
		t.Fatal("dangling symlink not reported")
	}
}

// fakeView records the events the app pushes to the page.
type fakeView struct{ events chan string }

func (v *fakeView) Bind(string, interface{}) error { return nil }
func (v *fakeView) Init(string)                    {}
func (v *fakeView) Eval(js string)                 { v.events <- js }
func (v *fakeView) Dispatch(fn func())             { fn() }
func (v *fakeView) Navigate(string)                {}
func (v *fakeView) LoadUI(string)                  {}
func (v *fakeView) SetSize(int, int)               {}
func (v *fakeView) Run()                           {}
func (v *fakeView) Destroy()                       {}

// waitEvent returns the first final event: done, error, cancelled or not-started.
func (v *fakeView) waitEvent(t *testing.T) string {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case js := <-v.events:
			for _, name := range []string{"enkoder-done", "enkoder-error", "enkoder-cancelled", "enkoder-not-started"} {
				if strings.Contains(js, `"`+name+`"`) {
					return name
				}
			}
		case <-timeout:
			t.Fatal("no final event")
		}
	}
}

func TestEncodeAsksBeforeOverwrite(t *testing.T) {
	ffmpegPath := findTool("ffmpeg")
	if ffmpegPath == "" {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command(ffmpegPath, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=5",
		"-t", "1", "-c:v", "mpeg4", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, out)
	}
	dst := filepath.Join(dir, "dst.mkv")
	if err := os.WriteFile(dst, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := confirmOverwrite
	defer func() { confirmOverwrite = orig }()

	view := &fakeView{events: make(chan string, 1024)}
	a := &app{ffmpegPath: ffmpegPath, view: view}
	job := jobDTO{Input: src, Output: dst, Video: []int{0}, Codec: "copy", Container: "mkv"}

	var asked string
	confirmOverwrite = func(p string) bool { asked = p; return false }
	if err := a.encode(job); err != nil {
		t.Fatal(err)
	}
	if ev := view.waitEvent(t); ev != "enkoder-not-started" {
		t.Fatalf("declined: event %s", ev)
	}
	if asked != dst {
		t.Fatalf("asked about %q, want %q", asked, dst)
	}
	if b, _ := os.ReadFile(dst); string(b) != "keep me" {
		t.Fatal("declined overwrite still changed the file")
	}

	confirmOverwrite = func(string) bool { return true }
	if err := a.encode(job); err != nil {
		t.Fatal(err)
	}
	if ev := view.waitEvent(t); ev != "enkoder-done" {
		t.Fatalf("accepted: event %s", ev)
	}
	if b, _ := os.ReadFile(dst); string(b) == "keep me" {
		t.Fatal("accepted overwrite left the old file")
	}

	// A new name is encoded without asking.
	confirmOverwrite = func(string) bool { t.Error("asked for a new file"); return false }
	job.Output = filepath.Join(dir, "new.mkv")
	if err := a.encode(job); err != nil {
		t.Fatal(err)
	}
	if ev := view.waitEvent(t); ev != "enkoder-done" {
		t.Fatalf("new file: event %s", ev)
	}
}

func TestRemovePartialOutput(t *testing.T) {
	dir := t.TempDir()

	// Created by the run: removed.
	fresh := filepath.Join(dir, "fresh.mkv")
	if err := os.WriteFile(fresh, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := removePartialOutput(fresh, nil); err != nil || !removed || fileExists(fresh) {
		t.Fatalf("fresh: removed=%v err=%v", removed, err)
	}

	// Older file ffmpeg never opened: kept.
	old := filepath.Join(dir, "old.mkv")
	if err := os.WriteFile(old, []byte("old film"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(old)
	if removed, err := removePartialOutput(old, before); err != nil || removed || !fileExists(old) {
		t.Fatalf("untouched: removed=%v err=%v", removed, err)
	}

	// Older file that the run truncated and rewrote: removed.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	before, _ = os.Stat(old)
	if err := os.WriteFile(old, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := removePartialOutput(old, before); err != nil || !removed {
		t.Fatalf("rewritten: removed=%v err=%v", removed, err)
	}

	// Nothing there: nothing to do.
	if removed, err := removePartialOutput(filepath.Join(dir, "none.mkv"), nil); err != nil || removed {
		t.Fatalf("missing: removed=%v err=%v", removed, err)
	}
}

func TestFailedEncodeKeepsUntouchedOutput(t *testing.T) {
	ffmpegPath := findTool("ffmpeg")
	if ffmpegPath == "" {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "broken.mkv")
	if err := os.WriteFile(src, []byte("not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.mkv")
	if err := os.WriteFile(dst, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := confirmOverwrite
	defer func() { confirmOverwrite = orig }()
	confirmOverwrite = func(string) bool { return true }

	view := &fakeView{events: make(chan string, 1024)}
	a := &app{ffmpegPath: ffmpegPath, view: view}
	job := jobDTO{Input: src, Output: dst, Video: []int{0}, Codec: "copy", Container: "mkv"}
	if err := a.encode(job); err != nil {
		t.Fatal(err)
	}
	if ev := view.waitEvent(t); ev != "enkoder-error" {
		t.Fatalf("event %s", ev)
	}
	if b, _ := os.ReadFile(dst); string(b) != "keep me" {
		t.Fatal("a run that never opened the output removed or changed it")
	}
}

func TestCancelledEncodeRemovesOutput(t *testing.T) {
	ffmpegPath := findTool("ffmpeg")
	if ffmpegPath == "" {
		t.Skip("no ffmpeg")
	}
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skip("no mkfifo")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mkv")
	gen := exec.Command(ffmpegPath, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=5",
		"-t", "2", "-c:v", "mpeg4", clip)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate clip: %v\n%s", err, out)
	}
	// The source is a FIFO that is never closed, so ffmpeg writes the
	// output and then waits for more input until it is cancelled.
	src := filepath.Join(dir, "src.mkv")
	if out, err := exec.Command("mkfifo", src).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v\n%s", err, out)
	}
	data, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		w, err := os.OpenFile(src, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = w.Write(data)
		time.Sleep(30 * time.Second)
		w.Close()
	}()

	dst := filepath.Join(dir, "dst.mkv")
	view := &fakeView{events: make(chan string, 4096)}
	a := &app{ffmpegPath: ffmpegPath, view: view}
	job := jobDTO{Input: src, Output: dst, Video: []int{0}, Codec: "copy", Container: "mkv", DurationSeconds: 2}
	if err := a.encode(job); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !fileExists(dst) {
		if time.Now().After(deadline) {
			t.Fatal("ffmpeg never created the output")
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.cancelEncode()
	if ev := view.waitEvent(t); ev != "enkoder-cancelled" {
		t.Fatalf("event %s", ev)
	}
	if fileExists(dst) {
		t.Fatal("incomplete output left after cancel")
	}
}

func TestDefaultSpeedIsBalanced(t *testing.T) {
	job := jobDTO{Input: "in.mkv", Output: "out.mkv", Video: []int{0}, Codec: "libx265", Container: "mkv"}
	opt, err := (&app{}).jobToOptions(job)
	if err != nil {
		t.Fatal(err)
	}
	if opt.Speed != ffmpeg.SpeedBalanced {
		t.Fatalf("default speed = %q, want balanced (as in the UI)", opt.Speed)
	}
	job.Speed = "quality"
	if opt, _ = (&app{}).jobToOptions(job); opt.Speed != ffmpeg.SpeedQuality {
		t.Fatalf("explicit quality = %q", opt.Speed)
	}
}

func TestFindQualityValidation(t *testing.T) {
	defer setLang(lang())
	setLang(langEN)
	src := filepath.Join(t.TempDir(), "film.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := jobDTO{Input: src, Output: src + ".out.mkv", Video: []int{0}, Codec: "copy", Container: "mkv", DurationSeconds: 600}
	a := &app{view: &fakeView{events: make(chan string, 16)}}
	a.metricOnce.Do(func() { a.metric = "xpsnr" })

	if err := a.findQuality(job, "high"); err == nil || !strings.Contains(err.Error(), "re-encoding") {
		t.Errorf("copy: %v", err)
	}
	job.Codec = "libx265"
	if err := a.findQuality(job, "superb"); err == nil || !strings.Contains(err.Error(), "superb") {
		t.Errorf("bad level: %v", err)
	}
	a.mu.Lock()
	a.busy = true
	a.mu.Unlock()
	if err := a.findQuality(job, "high"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("busy: %v", err)
	}

	b := &app{view: a.view}
	b.metricOnce.Do(func() {}) // no metric
	if err := b.findQuality(job, "high"); err == nil || !strings.Contains(err.Error(), "quality metric") {
		t.Errorf("no metric: %v", err)
	}
}
