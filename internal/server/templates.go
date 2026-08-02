package server

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed templates/*.html static/*
var webAssets embed.FS

var (
	webTemplate        = mustParseTemplate("templates/books.html")
	bookTemplate       = mustParseTemplate("templates/book.html")
	tasksTemplate      = mustParseTemplate("templates/tasks.html")
	taskDetailTemplate = mustParseTemplate("templates/task-detail.html")
	proofreadTemplate  = mustParseTemplate("templates/proofread.html")
	settingsTemplate   = mustParseTemplate("templates/settings.html")
	txtPreviewTemplate = mustParseTemplate("templates/txt-preview.html")
	kindleTemplate     = mustParseTemplate("templates/kindle.html")
	webStaticFiles     = mustSubFS(webAssets, "static")
)

func mustParseTemplate(name string) *template.Template {
	return template.Must(template.ParseFS(webAssets, name))
}

func mustSubFS(root fs.FS, directory string) fs.FS {
	result, err := fs.Sub(root, directory)
	if err != nil {
		panic(err)
	}
	return result
}
