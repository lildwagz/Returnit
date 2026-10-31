<#ftl output_format="HTML">
<#-- One layout for every e-mail: logo, white card, footer. Real e-mail HTML (tables, inline styles); dark clients
     get the dark palette through prefers-color-scheme. The logo is served by the download site (dl.<domain>/assets),
     whose address is derived from the link: https://auth.<domain>/realms/... -> https://dl.<domain>/assets. Without
     a usable link (the test e-mail) or when the image cannot load, the name "ReturnIt" shows instead. -->
<#assign assets = ((link!"")?keep_before("/realms/")?replace("://auth.", "://dl."))>
<#macro emailLayout title preheader="">
<!doctype html>
<html lang="${msg("emailLang")}" dir="${msg("emailDir")}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>${title}</title>
<style>@media (prefers-color-scheme: dark){.c-bg{background-color:#0B1220 !important;}.c-card{background-color:#131E33 !important;}.c-head{color:#F1F5F9 !important;}.c-body{color:#CBD5E1 !important;}.c-muted{color:#94A3B8 !important;}.c-line{border-color:#263552 !important;}.c-link{color:#60A5FA !important;}.c-btn{background-color:#60A5FA !important;}.c-btntext{color:#0B1220 !important;}.logo-light{display:none !important;}.logo-dark{display:block !important;}}</style>
</head>
<body class="c-bg" style="margin:0;padding:0;background:#F1F5F9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,'Noto Sans Arabic',sans-serif;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">${preheader}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="c-bg" style="background:#F1F5F9;">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;">
<tr><td style="padding:0 4px 16px;text-align:${msg("emailAlign")};"><#if assets?has_content && assets?starts_with("https://")><img class="logo-light" src="${assets}/assets/email-logo.png" width="150" alt="ReturnIt" style="display:block;border:0;height:auto;"><img class="logo-dark" src="${assets}/assets/email-logo-dark.png" width="150" alt="ReturnIt" style="display:none;border:0;height:auto;"><#else><span class="c-head" style="font-size:24px;font-weight:800;color:#17324D;">ReturnIt</span></#if></td></tr>
<tr><td class="c-card c-line" style="background:#FFFFFF;border:1px solid #E2E8F0;border-radius:20px;padding:32px 28px;text-align:${msg("emailAlign")};">
<h1 class="c-head" style="margin:0 0 16px;font-size:24px;line-height:1.3;font-weight:800;color:#17324D;">${title}</h1>
<#nested>
</td></tr>
<tr><td style="padding:20px 8px 0;text-align:${msg("emailAlign")};"><p class="c-muted" style="margin:0;font-size:12px;line-height:1.6;color:#5B6B80;">${msg("emailFooter")}</p></td></tr>
</table>
</td></tr></table>
</body></html>
</#macro>
<#macro para text><p class="c-body" style="margin:0 0 16px;font-size:16px;line-height:1.6;color:#334155;">${text}</p></#macro>
<#macro note text><p class="c-muted" style="margin:0;font-size:14px;line-height:1.6;color:#5B6B80;">${text}</p></#macro>
<#-- The call to action: a 48 px tall button, and the plain link under it for clients that block buttons. -->
<#macro action label url>
<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:8px 0 20px;"><tr><td class="c-btn" align="center" bgcolor="#2563EB" style="border-radius:12px;background:#2563EB;"><a class="c-btntext" href="${url}" style="display:inline-block;min-height:48px;line-height:48px;padding:0 28px;font-size:16px;font-weight:700;color:#FFFFFF;text-decoration:none;border-radius:12px;">${label}</a></td></tr></table>
<p class="c-muted" style="margin:0 0 16px;font-size:13px;line-height:1.5;color:#5B6B80;">${msg("emailFallback")}<br><a class="c-link" href="${url}" style="color:#2563EB;word-break:break-all;">${url}</a></p>
</#macro>
