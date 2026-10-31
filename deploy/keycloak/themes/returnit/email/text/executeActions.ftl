<#ftl output_format="plainText">
<#assign requiredActionsText><#if requiredActions??><#list requiredActions><#items as reqActionItem>${msg("requiredAction.${reqActionItem}")}<#sep>, </#sep></#items></#list></#if></#assign>
${msg("executeActionsTitle")}

${msg("executeActionsIntro", requiredActionsText)}
${msg("emailExpires", linkExpirationFormatter(linkExpiration))}

${msg("executeActionsButton")}:
${link}

${msg("executeActionsIgnore")}

--
${msg("emailFooter")}
