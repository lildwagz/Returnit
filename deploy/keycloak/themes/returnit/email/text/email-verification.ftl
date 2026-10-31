<#ftl output_format="plainText">
${msg("emailVerificationTitle")}

${msg("emailVerificationIntro")}
${msg("emailExpires", linkExpirationFormatter(linkExpiration))}

${msg("emailVerificationButton")}:
${link}

${msg("emailVerificationIgnore")}

--
${msg("emailFooter")}
