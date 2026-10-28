package releasecheck

import "strings"

// canonicalBody is the standardized Business Source License 1.1 text —
// Terms, Covenants of Licensor and Notice — reproduced verbatim from
// https://mariadb.com/bsl11/ (identical to the SPDX BUSL-1.1 text). The release
// gate compares the LICENSE file's body against this, so a reworded or truncated
// license cannot be published.
//
// This constant is independent of the LICENSE file on purpose: if it were read
// from LICENSE, the comparison would be circular and prove nothing.
const canonicalBody = `Terms
The Licensor hereby grants you the right to copy, modify, create derivative works, redistribute, and make non-production use of the Licensed Work. The Licensor may make an Additional Use Grant, above, permitting limited production use.
Effective on the Change Date, or the fourth anniversary of the first publicly available distribution of a specific version of the Licensed Work under this License, whichever comes first, the Licensor hereby grants you rights under the terms of the Change License, and the rights granted in the paragraph above terminate.
If your use of the Licensed Work does not comply with the requirements currently in effect as described in this License, you must purchase a commercial license from the Licensor, its affiliated entities, or authorized resellers, or you must refrain from using the Licensed Work.
All copies of the original and modified Licensed Work, and derivative works of the Licensed Work, are subject to this License. This License applies separately for each version of the Licensed Work and the Change Date may vary for each version of the Licensed Work released by Licensor.
You must conspicuously display this License on each original or modified copy of the Licensed Work. If you receive the Licensed Work in original or modified form from a third party, the terms and conditions set forth in this License apply to your use of that work.
Any use of the Licensed Work in violation of this License will automatically terminate your rights under this License for the current and all other versions of the Licensed Work.
This License does not grant you any right in any trademark or logo of Licensor or its affiliates (provided that you may use a trademark or logo of Licensor as expressly required by this License).
TO THE EXTENT PERMITTED BY APPLICABLE LAW, THE LICENSED WORK IS PROVIDED ON AN “AS IS” BASIS. LICENSOR HEREBY DISCLAIMS ALL WARRANTIES AND CONDITIONS, EXPRESS OR IMPLIED, INCLUDING (WITHOUT LIMITATION) WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, NON-INFRINGEMENT, AND TITLE.
MariaDB hereby grants you permission to use this License's text to license your works, and to refer to it using the trademark “Business Source License”, as long as you comply with the Covenants of Licensor below.
Covenants of Licensor
In consideration of the right to use this License's text and the “Business Source License” name and trademark, Licensor covenants to MariaDB, and to all other recipients of the licensed work to be provided by Licensor:
1. To specify as the Change License the GPL Version 2.0 or any later version, or a license that is compatible with GPL Version 2.0 or a later version, where “compatible” means that software provided under the Change License can be included in a program with software provided under GPL Version 2.0 or a later version. Licensor may specify additional Change Licenses without limitation.
2. To either: (a) specify an additional grant of rights to use that does not impose any additional restriction on the right granted in this License, as the Additional Use Grant; or (b) insert the text “None”.
3. To specify a Change Date.
4. Not to modify this License in any other way.
Notice
The Business Source License (this document, or the “License”) is not an Open Source license. However, the Licensed Work will eventually be made available under an Open Source License, as stated in this License.`

// normalizeEOL converts CRLF and lone CR line endings to LF. A Windows checkout
// (core.autocrlf) rewrites text files to CRLF, which must not be mistaken for a
// modified license: line endings are not words. Callers normalize before
// comparing or parsing so results are identical on every platform.
func normalizeEOL(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// licenseBody returns the standardized portion of a LICENSE file: everything
// from the "Terms" heading onward. The Parameters block above it is the part a
// licensor completes, so it is deliberately excluded from the text comparison.
// It expects end-of-line-normalized input.
func licenseBody(license string) string {
	if idx := strings.Index(license, "\nTerms\n"); idx >= 0 {
		return license[idx+1:]
	}
	if strings.HasPrefix(license, "Terms\n") {
		return license
	}
	return ""
}

// normalizeText collapses line wrapping, quote style and whitespace so the
// comparison does not depend on formatting. It never changes a word, so a
// reworded license still fails.
func normalizeText(text string) string {
	replacer := strings.NewReplacer(
		"\u2018", "'", "\u2019", "'",
		"\u201c", "\"", "\u201d", "\"",
	)
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}
