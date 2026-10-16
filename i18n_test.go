package main

import "testing"

func TestNormalizeLang(t *testing.T) {
	for in, want := range map[string]string{
		"pl_PL.UTF-8": langPL, "pl-PL": langPL, "PL": langPL,
		"en_US.UTF-8": langEN, "de-DE": langEN, "": langEN,
	} {
		if got := normalizeLang(in); got != want {
			t.Errorf("normalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMessagesComplete(t *testing.T) {
	for key := range messages[langEN] {
		if _, ok := messages[langPL][key]; !ok {
			t.Errorf("missing pl message %q", key)
		}
	}
	for key := range messages[langPL] {
		if _, ok := messages[langEN][key]; !ok {
			t.Errorf("missing en message %q", key)
		}
	}
}

func TestTr(t *testing.T) {
	defer setLang(lang())
	setLang(langPL)
	if got := tr("bad_cq", 51); got != "jakość CQ/CRF musi być w zakresie 0–51" {
		t.Errorf("pl: %q", got)
	}
	setLang(langEN)
	if got := tr("bad_cq", 63); got != "CQ/CRF quality must be in the range 0–63" {
		t.Errorf("en: %q", got)
	}
	if got := tr("no_such_key"); got != "no_such_key" {
		t.Errorf("unknown key: %q", got)
	}
}

func TestSetLanguageSaved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("AppData", dir)         // Windows
	defer setLang(lang())
	setLang(langPL)
	if err := (&app{}).setLanguage("en"); err != nil {
		t.Fatal(err)
	}
	setLang(langPL)
	initLang()
	if lang() != langEN {
		t.Errorf("after reload lang = %q, want en", lang())
	}
}
