// Package authmail renders the same accessible layout for account security mail.
package authmail

import (
	"bytes"
	"html/template"
	"strings"
)

type Content struct {
	Language, Direction, Domain, Title, Reason, Email, Previous, Code, Link, Button string
	CodeLabel, IPLabel, IP, TimeLabel, Time, Expiry, Ignore                         string
}

func Render(content Content) (string, string, error) {
	text := content.Title + " — " + content.Domain + "\n\n" + content.Reason + "\n" + content.Email + "\n"
	if content.Previous != "" {
		text += content.Previous + " → " + content.Email + "\n"
	}
	if content.Code != "" {
		text += "\n" + content.CodeLabel + "\n\n" + content.Code + "\n"
	}
	if content.Link != "" {
		text += "\n" + content.Expiry + "\n\n" + content.Button + ":\n" + content.Link + "\n"
	}
	text += "\n" + content.IPLabel + ": " + content.IP + "\n" + content.TimeLabel + ": " + content.Time + "\n\n" + content.Ignore
	var body bytes.Buffer
	if err := accountEmailTemplate.Execute(&body, content); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(text), body.String(), nil
}

var accountEmailTemplate = template.Must(template.New("account-email").Parse(mailTemplate))

const mailTemplate = `<!doctype html><html lang="{{.Language}}" dir="{{.Direction}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body style="margin:0;background:#f4f7f8;color:#52636d;font-family:Arial,sans-serif"><div style="max-width:600px;margin:0 auto;padding:24px 12px"><div style="background:#fff;border:1px solid #d9e2e5;border-radius:14px;padding:24px"><p style="margin:0;color:#087f8c;font-size:18px;font-weight:700">SiteBrush · {{.Domain}}</p><h1 style="color:#172126;font-size:24px;line-height:1.3">{{.Title}}</h1><p style="line-height:1.6">{{.Reason}}</p><p style="overflow-wrap:anywhere;color:#172126">{{if .Previous}}<strong dir="ltr">{{.Previous}}</strong><br>↓<br>{{end}}<strong dir="ltr">{{.Email}}</strong></p>{{if .Code}}<p style="margin:24px 0 6px">{{.CodeLabel}}</p><p style="margin:0 0 20px"><strong dir="ltr" style="display:inline-block;white-space:nowrap;font-family:monospace;font-size:32px;line-height:1.5;letter-spacing:3px;font-weight:800;color:#172126">{{.Code}}</strong></p>{{end}}<p style="font-size:14px;line-height:1.5">{{.Expiry}}</p>{{if .Link}}<p style="margin:24px 0"><a href="{{.Link}}" style="display:inline-block;max-width:100%;box-sizing:border-box;background:#087f8c;color:#fff;padding:12px 18px;border-radius:8px;font-weight:700;text-decoration:none;line-height:1.5">{{.Button}}</a></p>{{end}}<p style="font-size:13px;line-height:1.7">{{.IPLabel}}: <span dir="ltr">{{.IP}}</span><br>{{.TimeLabel}}: {{.Time}}</p><p style="font-size:14px;line-height:1.6;border-top:1px solid #d9e2e5;padding-top:16px">{{.Ignore}}</p></div></div></body></html>`
