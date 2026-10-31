<#ftl output_format="HTML">
<#import "template.ftl" as layout>
<@layout.emailLayout title=msg("emailTestTitle") preheader=msg("emailTestBody")>
<@layout.para msg("emailTestBody") />
</@layout.emailLayout>
