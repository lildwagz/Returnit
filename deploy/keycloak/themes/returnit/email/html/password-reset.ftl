<#ftl output_format="HTML">
<#import "template.ftl" as layout>
<@layout.emailLayout title=msg("passwordResetTitle") preheader=msg("passwordResetPreheader")>
<@layout.para msg("passwordResetIntro") />
<@layout.para msg("emailExpires", linkExpirationFormatter(linkExpiration)) />
<@layout.action msg("passwordResetButton") link />
<@layout.note msg("passwordResetIgnore") />
</@layout.emailLayout>
