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
		zenity.Title(tr("pick_open")),
		zenity.FileFilters{
			{Name: tr("filter_video"), Patterns: []string{"*.mkv", "*.mp4", "*.avi", "*.mov", "*.m4v", "*.ts", "*.m2ts", "*.webm"}},
			{Name: tr("filter_all"), Patterns: []string{"*.*"}},
		},
	)
	if err == zenity.ErrCanceled {
		return "", nil
	}
	return path, err
}

func pickSaveFile(filename, ext string) (string, error) {
	path, err := zenity.SelectFileSave(
		zenity.Title(tr("pick_save")),
		zenity.Filename(filename),
		zenity.FileFilters{
			{Name: strings.ToUpper(ext) + " (*." + ext + ")", Patterns: []string{"*." + ext}},
			{Name: tr("filter_all"), Patterns: []string{"*.*"}},
		},
		zenity.ConfirmOverwrite(),
	)
	if err == zenity.ErrCanceled {
		return "", nil
	}
	return path, err
}
