//go:build windows

package main

import (
	"strings"

	"github.com/ncruces/zenity"
)

func (a *app) openInput() (string, error) {
	return a.selectInputFile()
}

func (a *app) saveOutput(suggested, container string) (string, error) {
	return a.selectOutputFile(suggested, container)
}

func pickOpenFile() (string, error) {
	path, err := zenity.SelectFile(
		zenity.Title("Wybierz film"),
		zenity.FileFilters{
			{Name: "Wideo", Patterns: []string{"*.mkv", "*.mp4", "*.avi", "*.mov", "*.m4v", "*.ts", "*.m2ts", "*.webm"}},
			{Name: "Wszystkie pliki", Patterns: []string{"*.*"}},
		},
	)
	if err == zenity.ErrCanceled {
		return "", nil
	}
	return path, err
}

func pickSaveFile(filename, ext string) (string, error) {
	path, err := zenity.SelectFileSave(
		zenity.Title("Zapisz jako"),
		zenity.Filename(filename),
		zenity.FileFilters{
			{Name: strings.ToUpper(ext) + " (*." + ext + ")", Patterns: []string{"*." + ext}},
			{Name: "Wszystkie pliki", Patterns: []string{"*.*"}},
		},
		zenity.ConfirmOverwrite(),
	)
	if err == zenity.ErrCanceled {
		return "", nil
	}
	return path, err
}
