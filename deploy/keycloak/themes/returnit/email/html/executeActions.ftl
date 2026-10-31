<#ftl output_format="HTML">
<#outputformat "plainText">
<#assign requiredActionsText><#if requiredActions??><#list requiredActions><#items as reqActionItem>${msg("requiredAction.${reqActionItem}")}<#sep>, </#sep></#items></#list></#if></#assign>
</#outputformat>
<#import "template.ftl" as layout>
<@layout.emailLayout title=msg("executeActionsTitle") preheader=msg("executeActionsPreheader")>
<@layout.para msg("executeActionsIntro", requiredActionsText) />
<@layout.para msg("emailExpires", linkExpirationFormatter(linkExpiration)) />
<@layout.action msg("executeActionsButton") link />
<@layout.note msg("executeActionsIgnore") />
</@layout.emailLayout>
