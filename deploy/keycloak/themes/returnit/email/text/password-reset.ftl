<#ftl output_format="plainText">
${msg("passwordResetTitle")}

${msg("passwordResetIntro")}
${msg("emailExpires", linkExpirationFormatter(linkExpiration))}

${msg("passwordResetButton")}:
${link}

${msg("passwordResetIgnore")}

--
${msg("emailFooter")}
