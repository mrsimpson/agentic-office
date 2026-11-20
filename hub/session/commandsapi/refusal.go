package sessioncommandsv1

import (
	"errors"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
)

// RefusalError returns the error of a release that the hub refused because staged files quote
// sentences off the record: failed_precondition with r as its error detail.
func RefusalError(r *QuoteRefusal) error {
	ce := connect.NewError(connect.CodeFailedPrecondition,
		"the release is refused: staged files quote sentences that are off the record, word for word")
	d, err := connectproto.NewErrorDetail(r)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "encode the quote refusal: "+err.Error())
	}
	return ce.WithDetail(d)
}

// RefusalOf returns the QuoteRefusal detail of err, or nil when err is not a release refused
// because of quotes.
func RefusalOf(err error) *QuoteRefusal {
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeFailedPrecondition {
		return nil
	}
	for _, d := range ce.Details() {
		if m, err := connectproto.UnmarshalErrorDetail(d); err == nil {
			if r, ok := m.(*QuoteRefusal); ok {
				return r
			}
		}
	}
	return nil
}
