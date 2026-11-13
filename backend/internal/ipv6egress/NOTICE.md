# Source and local changes

Imported from the user-provided Sub2API share source marked `0.2.21-yoshub.29` on 2026-11-13. Its `SHARE_README_CN.md` identifies base commit `3b7f228947c6c3ecfdb09331792a92b053b622b6` plus share-package removals and privacy cleanup. No Git history was included, so the base identifier is attribution, not a claim of byte-for-byte repository identity.

Source paths imported: `backend/internal/ipv6egress`, `backend/internal/pkg/ovhipv6`, `backend/cmd/ipv6-egress`, and `deploy/ovh-ipv6-egress`. The source package and this repository contain the GNU Lesser General Public License version 3 in the root LICENSE. That license is retained.

Local changes add control-origin validation, token-file precedence, disabled control redirects, bounded JSON control messages, explicit client-CIDR and destination-host allowlists, tests, separate deployment paths, and compatibility documentation. The original custom fork's `tools/ipv6-egress` program and its existing control/state contract are preserved.
