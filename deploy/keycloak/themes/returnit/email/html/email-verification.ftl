<#ftl output_format="HTML">
<#import "template.ftl" as layout>
<@layout.emailLayout title=msg("emailVerificationTitle") preheader=msg("emailVerificationPreheader")>
<@layout.para msg("emailVerificationIntro") />
<@layout.para msg("emailExpires", linkExpirationFormatter(linkExpiration)) />
<@layout.action msg("emailVerificationButton") link />
<@layout.note msg("emailVerificationIgnore") />
</@layout.emailLayout>
