package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// UI languages. The page has its own dictionary (ui/index.html); this file
// covers the texts Go produces: errors, file dialogs, startup messages.
const (
	langPL = "pl"
	langEN = "en"
)

var currentLang atomic.Value // string

func lang() string {
	if l, ok := currentLang.Load().(string); ok {
		return l
	}
	return langEN
}

func setLang(l string) {
	currentLang.Store(normalizeLang(l))
}

// normalizeLang maps a locale such as "pl_PL.UTF-8" or "pl-PL" to a
// supported language; everything that is not Polish is English.
func normalizeLang(l string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "pl") {
		return langPL
	}
	return langEN
}

// initLang loads the saved choice, or falls back to the system locale.
func initLang() {
	if s, err := loadSettings(); err == nil && s.Lang != "" {
		setLang(s.Lang)
		return
	}
	setLang(systemLang())
}

type settings struct {
	Lang string `json:"lang"`
	// FFmpegPath is the "Custom ffmpeg path" setting: the ffmpeg binary or
	// its folder. Empty means search automatically.
	FFmpegPath string `json:"ffmpegPath,omitempty"`
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "enkoder", "settings.json"), nil
}

func loadSettings() (settings, error) {
	var s settings
	p, err := settingsPath()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

// updateSettings loads the saved settings, applies change and saves them,
// so each setting keeps the others intact.
func updateSettings(change func(*settings)) error {
	s, err := loadSettings()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// An unreadable file is replaced rather than blocking the change.
		s = settings{}
	}
	change(&s)
	return saveSettings(s)
}

func saveSettings(s settings) error {
	p, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// setLanguage is bound to the page; the switch in the header calls it.
func (a *app) setLanguage(l string) error {
	setLang(l)
	return updateSettings(func(s *settings) { s.Lang = lang() })
}

// tr returns the message for key in the current language, formatted with args.
func tr(key string, args ...interface{}) string {
	msg, ok := messages[lang()][key]
	if !ok {
		msg, ok = messages[langEN][key]
	}
	if !ok {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

var messages = map[string]map[string]string{
	langEN: {
		"busy":                    "an encode is already running",
		"no_input":                "no source file",
		"no_output":               "no output file",
		"no_video":                "select at least one video track",
		"same_file":               "the output file is the source file — choose a different name",
		"overwrite_q":             "The file already exists:\n%s\n\nOverwrite it?",
		"overwrite_ok":            "Overwrite",
		"overwrite_cancel":        "Cancel",
		"quality_needs_enc":       "a target quality needs re-encoding — choose NVENC, libx265 or SVT-AV1",
		"no_metric":               "this ffmpeg has no quality metric (libvmaf, xpsnr or ssim)",
		"bad_level":               "unknown quality level: %s",
		"no_duration":             "the source duration is unknown",
		"quality_start":           "Finding CRF: %s target %s, %d samples, starting at %d",
		"quality_step":            "CRF %d · sample %d/%d",
		"quality_trial":           "CRF %d: %s %s (worst sample) %s — samples: %s",
		"quality_found":           "Chosen CRF %d (%s %s), estimated video size %s",
		"quality_not_met":         "Target not reached even at CRF %d (%s %s) — this is the best tried",
		"partial_removed":         "Removed the incomplete output file: %s",
		"partial_remove_failed":   "Could not remove the incomplete output file %s: %v",
		"nvenc_missing":           "hevc_nvenc is not available — choose libx265 (CPU)",
		"svtav1_missing":          "libsvtav1 is not available in this ffmpeg",
		"scale_needs_enc":         "changing the resolution needs re-encoding — choose NVENC, libx265 or SVT-AV1",
		"deint_needs_enc":         "deinterlacing needs re-encoding — choose NVENC, libx265 or SVT-AV1",
		"bad_resolution":          "unsupported resolution: %d",
		"bad_cq":                  "CQ/CRF quality must be in the range 0–%d",
		"bad_master":              "invalid mastering display metadata: %s",
		"bad_maxcll":              "invalid MaxCLL metadata: %s",
		"bad_tune":                "unsupported tune: %s",
		"tune_x265_only":          "tune film/animation/grain only works with libx265",
		"bad_speed":               "unsupported speed profile: %s",
		"bad_pixfmt":              "unsupported pixel format: %s",
		"bad_preset":              "unsupported preset %q for %s",
		"pick_open":               "Choose a video",
		"pick_save":               "Save as",
		"filter_video":            "Video",
		"filter_all":              "All files",
		"ffpath_empty":            "no path given",
		"ffpath_not_absolute":     "the path must be absolute: %s",
		"ffpath_not_found":        "the path does not exist: %s",
		"ffpath_no_ffmpeg_in_dir": "no ffmpeg in the folder %s",
		"ffpath_not_executable":   "not an executable file: %s",
		"ffpath_not_ffmpeg":       "%s does not run as ffmpeg (%v)",
		"ffpath_fallback":         "The custom ffmpeg path cannot be used (%s) — using %s instead.",
		"ffpath_busy":             "wait for the current encode to finish",
		"pick_ffmpeg":             "Choose the ffmpeg program",
		"missing_tools":           "ffmpeg/ffprobe not found in PATH.\nInstall FFmpeg (e.g. sudo apt install ffmpeg)\nor put the binaries in /usr/bin or /usr/local/bin.",
		"missing_tools_win":       "ffmpeg/ffprobe not found in PATH.\nInstall FFmpeg and add its bin folder to PATH\nor put them in c:\\ffmpeg\\bin\\.",
		"webkit_failed":           "Could not start WebKitGTK.\nInstall the packages libwebkit2gtk-4.1 (or 4.0) and libgtk-3-0.",
		"webview2_failed":         "Could not start WebView2 (Edge Runtime).",
	},
	langPL: {
		"busy":                    "kodowanie już trwa",
		"no_input":                "brak pliku źródłowego",
		"no_output":               "brak pliku wynikowego",
		"no_video":                "wybierz co najmniej jedną ścieżkę video",
		"same_file":               "plik wynikowy to plik źródłowy — wybierz inną nazwę",
		"overwrite_q":             "Plik już istnieje:\n%s\n\nNadpisać go?",
		"overwrite_ok":            "Nadpisz",
		"overwrite_cancel":        "Anuluj",
		"quality_needs_enc":       "docelowa jakość wymaga ponownego kodowania — wybierz NVENC, libx265 lub SVT-AV1",
		"no_metric":               "ten ffmpeg nie ma metryki jakości (libvmaf, xpsnr ani ssim)",
		"bad_level":               "nieznany poziom jakości: %s",
		"no_duration":             "nieznana długość źródła",
		"quality_start":           "Dobieranie CRF: %s cel %s, %d próbek, start od %d",
		"quality_step":            "CRF %d · próbka %d/%d",
		"quality_trial":           "CRF %d: %s %s (najsłabsza próbka) %s — próbki: %s",
		"quality_found":           "Wybrane CRF %d (%s %s), szacowany rozmiar wideo %s",
		"quality_not_met":         "Celu nie osiągnięto nawet przy CRF %d (%s %s) — to najlepsza próba",
		"partial_removed":         "Usunięto niedokończony plik wynikowy: %s",
		"partial_remove_failed":   "Nie udało się usunąć niedokończonego pliku %s: %v",
		"nvenc_missing":           "hevc_nvenc niedostępny — wybierz libx265 (CPU)",
		"svtav1_missing":          "libsvtav1 niedostępny w tym ffmpeg",
		"scale_needs_enc":         "zmiana rozdzielczości wymaga ponownego kodowania — wybierz NVENC, libx265 lub SVT-AV1",
		"deint_needs_enc":         "deinterlace wymaga ponownego kodowania — wybierz NVENC, libx265 lub SVT-AV1",
		"bad_resolution":          "nieobsługiwana rozdzielczość: %d",
		"bad_cq":                  "jakość CQ/CRF musi być w zakresie 0–%d",
		"bad_master":              "nieprawidłowe metadane mastering display: %s",
		"bad_maxcll":              "nieprawidłowe metadane MaxCLL: %s",
		"bad_tune":                "nieobsługiwany tune: %s",
		"tune_x265_only":          "tune film/animation/grain działa tylko z libx265",
		"bad_speed":               "nieobsługiwany profil szybkości: %s",
		"bad_pixfmt":              "nieobsługiwany format pikseli: %s",
		"bad_preset":              "nieobsługiwany preset %q dla %s",
		"pick_open":               "Wybierz film",
		"pick_save":               "Zapisz jako",
		"filter_video":            "Wideo",
		"filter_all":              "Wszystkie pliki",
		"ffpath_empty":            "nie podano ścieżki",
		"ffpath_not_absolute":     "ścieżka musi być bezwzględna: %s",
		"ffpath_not_found":        "ścieżka nie istnieje: %s",
		"ffpath_no_ffmpeg_in_dir": "w folderze %s nie ma ffmpeg",
		"ffpath_not_executable":   "to nie jest plik wykonywalny: %s",
		"ffpath_not_ffmpeg":       "%s nie uruchamia się jako ffmpeg (%v)",
		"ffpath_fallback":         "Nie można użyć własnej ścieżki ffmpeg (%s) — używam %s.",
		"ffpath_busy":             "poczekaj, aż skończy się bieżące kodowanie",
		"pick_ffmpeg":             "Wybierz program ffmpeg",
		"missing_tools":           "Nie znaleziono ffmpeg/ffprobe w PATH.\nZainstaluj FFmpeg (np. sudo apt install ffmpeg)\nlub umieść binaria w /usr/bin albo /usr/local/bin.",
		"missing_tools_win":       "Nie znaleziono ffmpeg/ffprobe w PATH.\nZainstaluj FFmpeg i dodaj folder bin do PATH\nlub umieść je w c:\\ffmpeg\\bin\\.",
		"webkit_failed":           "Nie udało się uruchomić WebKitGTK.\nZainstaluj pakiety: libwebkit2gtk-4.1 (lub 4.0), libgtk-3-0.",
		"webview2_failed":         "Nie udało się uruchomić WebView2 (Edge Runtime).",
	},
}
