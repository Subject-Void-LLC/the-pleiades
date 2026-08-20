Added 15 security and cryptography filters, callable from `when`, `when_or`,
and `when_cel`: `sha256Hash`, `hmacGenerate`, `secureCompare` (constant-time),
`generateRandomPassword` (`crypto/rand`-backed), `parseJWTPayloadUnverified`
(reads claims without checking the signature; never treat the result as
authenticated), `parseX509Certificate`, `pemToDER`/`derToPEM`,
`sshPublicKeyToPEM`/`pemToSSHPublicKey`, `maskPII` (best-effort SSN/credit-card
/bearer-token redaction, not a compliance guarantee), `windowsSIDToHex`/
`hexToWindowsSID`, `parseDistinguishedName`, and `snmpOIDTranslate` (a small
curated MIB-II table). See the generated filter reference for the full list.
