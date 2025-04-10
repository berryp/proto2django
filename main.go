package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var caser = cases.Title(language.English)

// ProtoMessage represents a parsed protobuf message with its fields.
type ProtoMessage struct {
	Name   string
	Fields []ProtoField
}

// ProtoField represents a single field in a protobuf message.
type ProtoField struct {
	Name     string
	Type     string
	Repeated bool
}

// RenderedField represents a Django-compatible field derived from a protobuf field.
type RenderedField struct {
	Name       string
	Type       string
	Repeated   bool
	DjangoType string
}

// RenderedMessage is a Django-compatible message ready for template rendering.
type RenderedMessage struct {
	Name   string
	Fields []RenderedField
}

// TemplateData holds the overall context passed to the templates.
type TemplateData struct {
	AppName  string
	AppTitle string
	Messages []RenderedMessage
}

// PythonType maps a protobuf type to a Django model field.
func PythonType(protoType string) string {
	switch protoType {
	case "int32", "int64":
		return "models.IntegerField()"
	case "string":
		return "models.CharField(max_length=255)"
	case "bool":
		return "models.BooleanField()"
	case "float", "double":
		return "models.FloatField()"
	default:
		return "models.ForeignKey(" + protoType + ", on_delete=models.CASCADE)"
	}
}

// ParseProto reads and parses the .proto file into structured messages and fields.
func ParseProto(protoPath string) ([]ProtoMessage, error) {
	data, err := os.ReadFile(protoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read proto file: %w", err)
	}
	text := string(data)

	messageRe := regexp.MustCompile(`(?m)message\s+(\w+)\s*{([^}]*)}`)
	fieldRe := regexp.MustCompile(`(?m)(repeated\s+)?(\w+)\s+(\w+)\s*=\s*\d+`)

	matches := messageRe.FindAllStringSubmatch(text, -1)
	var messages []ProtoMessage

	for _, match := range matches {
		msgName := match[1]
		msgBody := match[2]
		fieldMatches := fieldRe.FindAllStringSubmatch(msgBody, -1)

		var fields []ProtoField
		for _, f := range fieldMatches {
			repeated := strings.TrimSpace(f[1]) == "repeated"
			typ := f[2]
			name := f[3]
			fields = append(fields, ProtoField{Name: name, Type: typ, Repeated: repeated})
		}
		messages = append(messages, ProtoMessage{Name: msgName, Fields: fields})
	}
	return messages, nil
}

// GenerateApp takes a .proto file and generates a Django app in the specified directory.
func GenerateApp(protoPath, outputDir string) error {
	rawMessages, err := ParseProto(protoPath)
	if err != nil {
		return err
	}

	var rendered []RenderedMessage
	for _, msg := range rawMessages {
		var fields []RenderedField
		for _, f := range msg.Fields {
			fields = append(fields, RenderedField{
				Name:       f.Name,
				Type:       f.Type,
				Repeated:   f.Repeated,
				DjangoType: PythonType(f.Type),
			})
		}
		rendered = append(rendered, RenderedMessage{Name: msg.Name, Fields: fields})
	}

	appName := filepath.Base(outputDir)
	data := TemplateData{
		AppName:  appName,
		AppTitle: caser.String(appName),
		Messages: rendered,
	}

	if err := os.MkdirAll(filepath.Join(outputDir, "migrations"), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create migrations directory: %w", err)
	}
	writeFile(filepath.Join(outputDir, "migrations", "__init__.py"), "")
	writeFile(filepath.Join(outputDir, "__init__.py"), "")
	writeFile(filepath.Join(outputDir, "tests.py"), "# placeholder\n")

	files := map[string]string{
		"models.py":      modelsTemplate,
		"serializers.py": serializersTemplate,
		"viewsets.py":    viewsetsTemplate,
		"urls.py":        urlsTemplate,
		"admin.py":       adminTemplate,
		"apps.py":        appsTemplate,
	}
	for name, tmpl := range files {
		if err := renderToFile(tmpl, data, filepath.Join(outputDir, name)); err != nil {
			return fmt.Errorf("failed to render %s: %w", name, err)
		}
	}

	return nil
}

// writeFile creates or overwrites a file with the given content.
func writeFile(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0644)
}

// renderToFile renders a text/template with provided data and writes to file.
func renderToFile(content string, data TemplateData, outputPath string) error {
	tmpl, err := template.New("template").Funcs(funcMap).Parse(content)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()
	return tmpl.Execute(file, data)
}

// funcMap defines custom template functions.
var funcMap = template.FuncMap{
	"ToLower": strings.ToLower,
}

// Templates

const modelsTemplate = `from django.db import models

{{- range .Messages }}
class {{ .Name }}(models.Model):
{{- if not .Fields }}
    pass
{{- else }}
{{- range .Fields }}
    {{ .Name }} = {{ .DjangoType }}
{{- end }}
{{- end }}
{{ end }}
`

const serializersTemplate = `from rest_framework import serializers
{{ range .Messages }}
from .models import {{ .Name }}
{{ end }}

{{ range .Messages }}
class {{ .Name }}Serializer(serializers.ModelSerializer):
    class Meta:
        model = {{ .Name }}
        fields = '__all__'
{{ end }}
`

const viewsetsTemplate = `from rest_framework import viewsets
{{ range .Messages }}
from .models import {{ .Name }}
from .serializers import {{ .Name }}Serializer
{{ end }}

{{ range .Messages }}
class {{ .Name }}ViewSet(viewsets.ModelViewSet):
    queryset = {{ .Name }}.objects.all()
    serializer_class = {{ .Name }}Serializer
{{ end }}
`

const urlsTemplate = `from django.urls import path, include
from rest_framework.routers import DefaultRouter
{{ range .Messages }}
from .viewsets import {{ .Name }}ViewSet
{{ end }}

router = DefaultRouter()
{{ range .Messages }}
router.register(r'{{ .Name | ToLower }}', {{ .Name }}ViewSet)
{{ end }}

urlpatterns = [
    path('', include(router.urls)),
]
`

const adminTemplate = `from django.contrib import admin
{{ range .Messages }}
from .models import {{ .Name }}
{{ end }}

{{ range .Messages }}
admin.site.register({{ .Name }})
{{ end }}
`

const appsTemplate = `from django.apps import AppConfig

class {{ .AppTitle }}Config(AppConfig):
    default_auto_field = 'django.db.models.BigAutoField'
    name = '{{ .AppName }}'
`

// main is the entry point of the CLI application.
func main() {
	var protoPath, outputDir string

	flag.StringVar(&protoPath, "proto", "", "Path to the .proto file")
	flag.StringVar(&outputDir, "out", "generated_app", "Output directory for Django app")
	flag.Parse()

	if protoPath == "" {
		log.Fatal("Please provide a .proto file with -proto flag")
	}

	if err := GenerateApp(protoPath, outputDir); err != nil {
		log.Fatalf("Error: %v", err)
	}

	fmt.Println("✅ Django app generated at", outputDir)
}
