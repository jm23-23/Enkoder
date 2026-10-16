//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Zenity 4 on GNOME Wayland uses the Nautilus file-chooser portal.
// The dialog opens and the user can pick a file, but zenity often exits
// with code 1 / empty stdout — ncruces then reports cancel, so the
// source field never updates. Force the in-process GtkFileChooser
// (GDK_DEBUG=no-portals) and write the path into #inputPath via Eval.

func (a *app) openInput() (string, error) {
	go a.finishPick("input", a.selectInputFile)
	return "", nil
}

func (a *app) saveOutput(suggested, container string) (string, error) {
	go a.finishPick("output", func() (string, error) {
		return a.selectOutputFile(suggested, container)
	})
	return "", nil
}

func (a *app) finishPick(kind string, fn func() (string, error)) {
	path, err := fn()
	pickLog("finishPick kind=%s path=%q err=%v", kind, path, err)
	if err != nil {
		a.emitEvent("enkoder-log", err.Error())
		return
	}
	if path == "" {
		pickLog("finishPick empty (cancel or zenity returned nothing)")
		return
	}
	if kind == "output" {
		a.applyPicked("output", path)
		return
	}
	dto, perr := a.probe(path)
	suggested := dto.SuggestedOutput
	if perr != nil {
		pickLog("probe err=%v", perr)
		a.applyPickedInput(path, probeDTO{}, "", perr)
		return
	}
	a.applyPickedInput(path, dto, suggested, nil)
}

func (a *app) applyPickedInput(path string, dto probeDTO, suggested string, probeErr error) {
	pathJ, _ := json.Marshal(path)
	dtoJ, _ := json.Marshal(dto)
	sugJ, _ := json.Marshal(suggested)
	errJ := []byte("null")
	if probeErr != nil {
		errJ, _ = json.Marshal(probeErr.Error())
	}
	js := fmt.Sprintf(`(function(){
  var p = %s, res = %s, sug = %s, err = %s;
  var el = document.getElementById("inputPath");
  if (el) el.value = p;
  if (typeof window.applyPickedInput === "function") window.applyPickedInput(p, res, sug, err);
})()`, string(pathJ), string(dtoJ), string(sugJ), string(errJ))
	pickLog("Eval applyPickedInput jsLen=%d", len(js))
	a.view.Dispatch(func() {
		a.view.Eval(js)
	})
}

func (a *app) applyPicked(kind, path string) {
	payload, err := json.Marshal(path)
	if err != nil {
		return
	}
	id := "inputPath"
	fn := "usePickedInput"
	ev := "enkoder-input-picked"
	if kind == "output" {
		id = "outputPath"
		fn = "usePickedOutput"
		ev = "enkoder-output-picked"
	}
	js := fmt.Sprintf(`(function(){
  var p = %s;
  var el = document.getElementById(%q);
  if (el) el.value = p;
  if (typeof window[%q] === "function") { window[%q](p); }
  else { window.dispatchEvent(new CustomEvent(%q, {detail: p})); }
})()`, string(payload), id, fn, fn, ev)
	pickLog("Eval applyPicked kind=%s jsLen=%d", kind, len(js))
	a.view.Dispatch(func() {
		a.view.Eval(js)
	})
}

func pickOpenFile() (string, error) {
	return runZenityFile(false, "Wybierz film", "", []string{
		"--file-filter=Wideo | *.mkv *.mp4 *.avi *.mov *.m4v *.ts *.m2ts *.webm",
		"--file-filter=Wszystkie pliki | *",
	})
}

func pickSaveFile(filename, ext string) (string, error) {
	return runZenityFile(true, "Zapisz jako", filename, []string{
		fmt.Sprintf("--file-filter=%s | *.%s", strings.ToUpper(ext), ext),
		"--file-filter=Wszystkie pliki | *",
	})
}

func runZenityFile(save bool, title, filename string, extra []string) (string, error) {
	args := []string{"--file-selection", "--title", title}
	if save {
		args = append(args, "--save")
	}
	if filename != "" {
		args = append(args, "--filename", filename)
	}
	args = append(args, extra...)
	cmd := exec.Command("zenity", args...)
	cmd.Env = append(os.Environ(), "GDK_DEBUG=no-portals", "GTK_USE_PORTAL=0")
	pickLog("zenity args=%v", args)
	out, err := cmd.Output()
	out = bytes.TrimSpace(out)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			pickLog("zenity exit=%d stdout=%q stderr=%q", ee.ExitCode(), out, bytes.TrimSpace(ee.Stderr))
			if ee.ExitCode() == 1 {
				return "", nil
			}
		} else {
			pickLog("zenity err=%v stdout=%q", err, out)
		}
		return "", err
	}
	pickLog("zenity ok stdout=%q", out)
	return string(out), nil
}

func pickLog(format string, args ...interface{}) {
	f, err := os.OpenFile("/tmp/enkoder-pick.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line := time.Now().Format("15:04:05.000") + " " + fmt.Sprintf(format, args...) + "\n"
	_, _ = f.WriteString(line)
}
