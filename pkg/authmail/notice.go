package authmail

import (
	"bytes"
	"html/template"
	"strings"
)

type NoticeLink struct{ Label, URL string }
type Notice struct {
	Language, Direction, Title, Introduction, Reason, Button, Details, DetailsURL string
	Links                                                                         []NoticeLink
}

// Notices share the account mail's visual vocabulary without displaying login
// codes, expiry claims, or request-IP fields that have no meaning for a release.
func RenderNotice(notice Notice) (string, string, error) {
	text := notice.Title + "\n\n" + notice.Introduction + "\n\n" + notice.Reason + "\n"
	for _, link := range notice.Links {
		text += "\n" + link.Label + "\n" + notice.Button + ": " + link.URL + "\n"
	}
	text += "\n" + notice.Details + ": " + notice.DetailsURL
	markup, err := template.New("notice").Parse(noticeTemplate)
	if err != nil {
		return "", "", err
	}
	var result bytes.Buffer
	if err := markup.Execute(&result, notice); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(text), result.String(), nil
}

const noticeTemplate = `<!doctype html><html lang="{{.Language}}" dir="{{.Direction}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body style="margin:0;background:#f4f7f8;color:#52636d;font-family:Arial,sans-serif"><div style="max-width:600px;margin:0 auto;padding:24px 12px"><div style="background:#fff;border:1px solid #d9e2e5;border-radius:14px;padding:24px"><p style="margin:0;color:#087f8c;font-size:18px;font-weight:700">SiteBrush · sitebrush.com</p><h1 style="color:#172126;font-size:24px;line-height:1.3">{{.Title}}</h1><p style="line-height:1.6">{{.Introduction}}</p><p style="line-height:1.6">{{.Reason}}</p>{{range .Links}}<section style="border-top:1px solid #d9e2e5;padding-top:16px;margin-top:20px"><p dir="ltr" style="overflow-wrap:anywhere;color:#172126">{{.Label}}</p><a href="{{.URL}}" style="display:inline-block;max-width:100%;box-sizing:border-box;background:#087f8c;color:#fff;padding:12px 18px;border-radius:8px;font-weight:700;text-decoration:none;line-height:1.5">{{$.Button}}</a></section>{{end}}<p style="font-size:14px;line-height:1.6;margin-top:24px"><a href="{{.DetailsURL}}" style="color:#087f8c">{{.Details}}</a></p></div></div></body></html>`
