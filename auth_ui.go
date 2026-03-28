package launchdock

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed web/auth_success.html
var authSuccessHTML string

//go:embed web/auth_error.html
var authErrorHTML string

type authPageData struct {
	Provider string
	Title    string
	Message  string
	Detail   string
}

var (
	authSuccessTemplate = template.Must(template.New("auth_success").Parse(authSuccessHTML))
	authErrorTemplate   = template.Must(template.New("auth_error").Parse(authErrorHTML))
)

func writeAuthSuccess(w http.ResponseWriter, provider, title, message string) {
	writeAuthTemplate(w, http.StatusOK, authSuccessTemplate, authPageData{
		Provider: provider,
		Title:    title,
		Message:  message,
	})
}

func writeAuthError(w http.ResponseWriter, status int, provider, title, message, detail string) {
	writeAuthTemplate(w, status, authErrorTemplate, authPageData{
		Provider: provider,
		Title:    title,
		Message:  message,
		Detail:   detail,
	})
}

func writeAuthTemplate(w http.ResponseWriter, status int, tmpl *template.Template, data authPageData) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		http.Error(w, data.Message, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
