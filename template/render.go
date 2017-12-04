package template

import (
	"html/template"

	"github.com/gin-gonic/gin/render"
)

// RenderAsset attempts to render an asset.
//
// This function reads an asset which was compiled using go-bindata and
// treats is as a golang html/template template.
//
// The result is then formatted in the generic render.Render interface
// gin uses to render templates.
func RenderAsset(name string, data interface{}) (render.Render, error) {
	d, err := Asset(name)
	if err != nil {
		return nil, err
	}

	t, err := template.New(name).Parse(string(d))
	if err != nil {
		return nil, err
	}

	return &render.HTML{Template: t, Data: data, Name: name}, nil
}
