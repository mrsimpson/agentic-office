package portal

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/mrsimpson/agentic-office/hub/portal/web"
)

// PreviewPage is one rendered fixture page of the portal (see Preview).
type PreviewPage struct {
	// Name is the file name of the page, for example "portal-wiki-index.html".
	Name string
	// Title says what the fixture shows.
	Title string
	// Path is the address that the real page has. The preview serves the page there, so the links
	// between the fixture pages work.
	Path string
	// HTML is the page.
	HTML []byte
}

// Preview renders every fixture page of the portal with the templates t, through the code of the
// real portal: the same frame, the same navigation and the same page template. It is the data of
// the dev preview of office-hub (see the subcommand preview); no hub, session or token is involved.
// It returns every page that rendered and, with them, one error that lists every page that did not.
func Preview(t *web.Templates) ([]PreviewPage, error) {
	var pages []PreviewPage
	var errs []error
	for _, f := range previewPages() {
		var b bytes.Buffer
		if err := t.Page.Execute(&b, withFrame(f.me, f.active, f.tenant, f.data)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.name, err))
			continue
		}
		pages = append(pages, PreviewPage{Name: f.name, Title: f.title, Path: f.active, HTML: b.Bytes()})
	}
	for _, f := range previewErrors() {
		var b bytes.Buffer
		if err := t.ErrorPage.Execute(&b, map[string]any{"Title": f.heading, "Message": f.message}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.name, err))
			continue
		}
		pages = append(pages, PreviewPage{Name: f.name, Title: f.title, HTML: b.Bytes()})
	}
	return pages, errors.Join(errs...)
}
