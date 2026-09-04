# Apple Developer ID intermediate

`DeveloperIDCA.cer` is Apple's public Developer ID G1 intermediate, downloaded
from https://www.apple.com/certificateauthority/DeveloperIDCA.cer.

SHA-256 (DER): `7afc9d01a62f03a2de9637936d4afe68090d2de18d03f29c88cfb0b1ba63587f`.
Expires February 1, 2027. This is a public certificate, not a private key.

The release identity issued under this intermediate needs the G1 chain even
when a clean host includes the newer G2 intermediate. Apple documents this
requirement at https://developer.apple.com/support/developer-id-intermediate-certificate/.
The signing harness checks the digest and imports the certificate into its
temporary keychain with normal system trust evaluation. It does not install
trust overrides. A future identity renewal may require a different chain.
