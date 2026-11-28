package stage

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"

	"github.com/mrsimpson/agentic-office/hub/stage/web"
)

// PreviewPage is one rendered fixture page of the stage (see Preview).
type PreviewPage struct {
	// Name is the file name of the page, for example "stage-live.html".
	Name string
	// Title says what the fixture shows.
	Title string
	// Path is the address that the real page has, or "" for a page that has none.
	Path string
	// HTML is the page.
	HTML []byte
}

// Preview renders every fixture page of the stage with the templates t, through the code of the
// real stage: the same view, the same region templates and the same page. It is the data of the
// dev preview of office-hub (see the subcommand preview); no stream, session or token is involved.
// It returns every page that rendered and, with them, one error that lists every page that did not.
func Preview(t *web.Templates) ([]PreviewPage, error) {
	var pages []PreviewPage
	var errs []error
	for _, f := range previewSessions() {
		regions := map[string]template.HTML{}
		failed := false
		for _, r := range web.Regions {
			html, err := f.view.renderRegion(t.Regions, r)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: region %s: %w", f.name, r, err))
				failed = true
				continue
			}
			regions[r] = template.HTML(html) // rendered by html/template above
		}
		if failed {
			continue
		}
		// The page has no script: it needs the event stream, which the preview does not have.
		var b bytes.Buffer
		if err := executePage(&b, t.Page, f.session, "preview", f.portal, regions, ""); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.name, err))
			continue
		}
		pages = append(pages, PreviewPage{Name: f.name, Title: f.title, Path: web.PathPrefix + f.session, HTML: b.Bytes()})
	}
	for _, f := range previewErrors() {
		var b bytes.Buffer
		if err := t.ErrorPage.Execute(&b, map[string]any{"Title": f.heading, "Message": f.message, "Status": f.status}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.name, err))
			continue
		}
		pages = append(pages, PreviewPage{Name: f.name, Title: f.title, HTML: b.Bytes()})
	}
	return pages, errors.Join(errs...)
}
