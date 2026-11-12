package stage

import (
	"bytes"
	"context"
	"html"
	"path"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
)

// Artifacts reads artifacts from the archive in-process (if-archive-read). archive.ReadService
// implements it. The stage reads only paths that the log of its own session names.
type Artifacts interface {
	Load(ctx context.Context, req *archivev1.ReadFileRequest) (*archivev1.ReadFileResponse, error)
}

// paperCSS styles the document inside its sandboxed frame.
const paperCSS = `body{font:16px/1.6 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;color:#151a21;background:#fff;margin:0;padding:1.6rem 2rem;max-width:60rem}
h1,h2,h3{line-height:1.25;letter-spacing:-.01em}h1{font-size:1.8rem}h2{font-size:1.4rem}code,pre{font-family:ui-monospace,Menlo,Consolas,monospace;background:#f2f4f8;border-radius:6px}
code{padding:.1em .3em}pre{padding:.8rem 1rem;overflow:auto;white-space:pre-wrap}pre code{padding:0;background:none}blockquote{margin-left:0;padding-left:1rem;border-left:3px solid #c5cbd6;color:#4a5361}
table{border-collapse:collapse}td,th{border:1px solid #d5dae3;padding:.3rem .6rem}`

// markdown renders Markdown without raw HTML: what an author writes cannot become markup.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// loadFile loads a Markdown or YAML file of the archive and renders it as an HTML document for a
// sandboxed frame. A schema decides the rendering later (dec-artifacts); a Markdown file without
// a schema is shown as text with formatting, and a YAML file as it is.
func loadFile(ctx context.Context, p string, files Artifacts) artifactView {
	av := artifactView{ID: p, Kind: "file"}
	if files == nil {
		av.Note = "The stage cannot read the archive."
		return av
	}
	res, err := files.Load(ctx, &archivev1.ReadFileRequest{Path: p})
	switch {
	case connect.CodeOf(err) == connect.CodeNotFound:
		av.Note = "There is no artifact " + p + "."
		return av
	case connect.CodeOf(err) == connect.CodeInvalidArgument:
		av.Note = "The stage cannot show the artifact " + p + "."
		return av
	case err != nil:
		av.Note = "The artifact " + p + " could not be loaded."
		return av
	}
	data := res.GetFile().GetContent()
	var body string
	switch path.Ext(p) {
	case ".md":
		av.Kind = "document"
		var b bytes.Buffer
		if err := markdown.Convert(data, &b); err != nil {
			av.Note = "The artifact " + p + " could not be rendered."
			return av
		}
		body = b.String()
	default:
		av.Kind = "data"
		body = "<pre>" + html.EscapeString(strings.TrimRight(string(data), "\n")) + "</pre>"
	}
	av.MediaType = "text/html"
	av.Content = "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><style>" + paperCSS + "</style></head><body>" + body + "</body></html>"
	return av
}
