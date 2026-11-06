package artifacts

import (
	"bytes"
	"html/template"
	"strings"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
)

// SlidesMediaType is the media type of rendered slides.
const SlidesMediaType = "text/html; charset=utf-8"

// The slide deck is one HTML document without scripts or outside resources. html/template escapes
// all text of the deck. The Content-Security-Policy allows only the inline style, so even a
// mistake in the escaping cannot load or run anything.
var deckTemplate = template.Must(template.New("deck").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
<title>{{.DocTitle}}</title>
<style>
html{scroll-snap-type:y mandatory;background:#000}
body{margin:0;font-family:system-ui,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:#000}
.slide{box-sizing:border-box;min-height:100vh;padding:6vh 6vw 10vh;background:#fff;border-bottom:0.5vh solid #000;position:relative;scroll-snap-align:start;break-after:page;overflow-wrap:anywhere}
.slide h1{font-size:7vw;line-height:1.15;margin:20vh 0 0}
.slide h2{font-size:5vw;line-height:1.15;margin:0 0 4vh}
.slide ul{margin:0;padding-left:1.2em;font-size:3.4vw;line-height:1.35}
.slide li{margin:0 0 1.5vh}
.slide .deck{font-size:2.2vw;margin:2vh 0 0;color:#222}
.slide footer{position:absolute;right:6vw;bottom:3vh;font-size:2vw;color:#222}
.notes{display:none}
</style>
</head>
<body data-slides="{{len .Slides}}">
{{- if .Title}}
<section class="slide title" id="title">
<h1>{{.Title}}</h1>
</section>
{{- end}}
{{- range .Slides}}
<section class="slide" id="slide-{{.N}}" aria-label="Slide {{.N}} of {{$.Count}}">
<h2>{{.Title}}</h2>
{{- if .Bullets}}
<ul>
{{- range .Bullets}}
<li>{{.}}</li>
{{- end}}
</ul>
{{- end}}
{{- if .Notes}}
<aside class="notes">{{.Notes}}</aside>
{{- end}}
<footer>{{if $.Title}}{{$.Title}} · {{end}}{{.N}} / {{$.Count}}</footer>
</section>
{{- end}}
</body>
</html>
`))

type slideView struct {
	N       int
	Title   string
	Bullets []string
	Notes   string
}

type deckView struct {
	DocTitle string
	Title    string
	Count    int
	Slides   []slideView
}

// RenderSlides renders a deck as one HTML document. It is deterministic: the same title and
// slides always give the same bytes. The document has no time, no random value and nothing from
// outside the deck.
func RenderSlides(title string, slides []*officev1.Slide) ([]byte, error) {
	v := deckView{Title: strings.TrimSpace(title), Count: len(slides)}
	for i, s := range slides {
		sv := slideView{N: i + 1, Title: strings.TrimSpace(s.GetTitle()), Notes: strings.TrimSpace(s.GetNotes())}
		for _, b := range s.GetBullets() {
			if b = strings.TrimSpace(b); b != "" {
				sv.Bullets = append(sv.Bullets, b)
			}
		}
		v.Slides = append(v.Slides, sv)
	}
	v.DocTitle = v.Title
	if v.DocTitle == "" && len(v.Slides) > 0 {
		v.DocTitle = v.Slides[0].Title
	}
	var buf bytes.Buffer
	if err := deckTemplate.Execute(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
