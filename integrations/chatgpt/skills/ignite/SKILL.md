---
name: ignite
description: Use when someone opens Ignite or asks to create a SiteBrush website, including on their own server.
---

# SiteBrush Ignite

Begin a new Ignite conversation in the user's language with a short introduction, even if the user has only opened the plugin. Explain that SiteBrush creates and edits websites, and can also copy an existing site while preserving its design. Explain what will be created: a website with an address and an editable starting page. Do not start by asking for a site name.

Offer two clear choices:

1. Create a public site on sitebrush.com. Ignite checks a short name, asks the person to solve CAPTCHA in a browser, sends a six-digit email code, and creates the site after verification. The assistant can then help connect the site's private editor.
2. Install the SiteBrush server application on the person's own server and create the site there. Ask whether they already have a server. If they do, ask for its operating system, architecture, and intended domain. Guide them to the matching build at https://github.com/matveynator/sitebrush#downloads and through https://github.com/matveynator/sitebrush#install-the-server-version, one step at a time. If they do not have a server, explain the sitebrush.com option. Do not imply that SiteBrush has been installed or that you have server access without evidence.

Ask which choice the person prefers. If they already chose one, acknowledge it and continue that path. If `ignite_get_started` is available, use it to confirm the current server and choices. If it is absent from a cached tool list, still give this introduction.

If SiteBrush is already installed on their server and they want Ignite registration there, pass their selected server domain in `server` on every read-only check and registration call. Check a name before `ignite_begin`. Verify that the returned `server` and full site domain belong to the selected server. If they do not, stop before beginning registration and explain the mismatch. Do not promise that any named domain supports Ignite until an actual check succeeds. If the server lacks Ignite, help install or enable the server application instead.

For online creation, ask for the short name only after the hosting choice. Explain that name availability is not a reservation. Ask for confirmation before `ignite_begin`. Explain each stage in plain language: CAPTCHA checks for a person; the email code confirms the mailbox; neither creates a site. Give the browser CAPTCHA link and use `ignite_status` when the person returns. Request the email and call `ignite_send_code`. Give the returned `confirmation_url` and ask the person to enter the code there, directly on SiteBrush. Use `ignite_status` afterward to confirm `status: complete`. Do not ask for the code in chat when this browser link is available. If a client blocks direct `ignite_complete`, retrieve `confirmation_url` with `ignite_status` for the same attempt. Never suggest resending the same code to bypass a client safety decision. Only say the site was created when the status confirms completion and the domain belongs to the chosen server.

After creation, use the returned MCP endpoint only when it is ready. Help the owner connect the site's personal editor with OAuth. Ignite itself does not edit existing sites.
