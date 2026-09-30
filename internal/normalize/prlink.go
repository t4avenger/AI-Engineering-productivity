package normalize

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// prLinkURLPattern matches a bare http(s) URL token; prLinkPathPattern then
// confirms the path is a pull/merge-request permalink. Kept as two steps so a
// URL embedded in surrounding command/output text is still recognised.
var (
	prLinkURLPattern  = regexp.MustCompile(`https?://[^\s"'<>\\\]\[(){}]+`)
	prLinkPathPattern = regexp.MustCompile(`(?i)/(?:pull|pulls|pull-requests|pullrequest)/[1-9][0-9]*$|/-/merge_requests/[1-9][0-9]*$`)
)

// AttachPRLinkEvidence records only pull/merge-request URLs present verbatim in
// the reviewed provider fields named by scanFields. It recognises common
// pull/merge-request paths across GitHub, GitLab, Bitbucket, Azure DevOps, and
// compatible self-hosted hosts; it neither calls the host nor derives a URL
// from repository metadata (honesty invariant: no fabricated link).
//
// When at least one candidate is found it sets attributes["pr_link_candidates"]
// (sorted, unique URLs) and extensions["pr_link_evidence"] (field+url
// provenance). A genuine absence writes nothing, so no empty slice or
// fabricated value is emitted. It is provider-neutral: Claude and Codex share it
// with their own scanFields so the URL grammar lives in exactly one place.
func AttachPRLinkEvidence(attributes, extensions, fields map[string]any, scanFields []string) {
	seen := map[string]struct{}{}
	evidence := make([]map[string]string, 0)
	for _, field := range scanFields {
		text, ok := ObservedString(fields[field])
		if !ok {
			continue
		}
		for _, candidate := range PRLinkURLs(text) {
			key := field + "\x00" + candidate
			if _, found := seen[key]; found {
				continue
			}
			seen[key] = struct{}{}
			evidence = append(evidence, map[string]string{"field": field, "url": candidate})
		}
	}
	if len(evidence) == 0 {
		return
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i]["url"] == evidence[j]["url"] {
			return evidence[i]["field"] < evidence[j]["field"]
		}
		return evidence[i]["url"] < evidence[j]["url"]
	})
	candidates := make([]string, 0, len(evidence))
	unique := map[string]struct{}{}
	for _, item := range evidence {
		if _, found := unique[item["url"]]; found {
			continue
		}
		unique[item["url"]] = struct{}{}
		candidates = append(candidates, item["url"])
	}
	attributes["pr_link_candidates"] = candidates
	extensions["pr_link_evidence"] = evidence
}

// PRLinkScanText renders a retained provider value as the text
// AttachPRLinkEvidence scans. A string passes through when observed; a
// structured value (a decoded tool input or result object/array) is rendered as
// compact JSON with HTML escaping off, so a URL's `&` survives verbatim. Any
// other value (number, bool, nil) carries no URL and reports false. It lets a
// provider scan structured tool I/O without a second copy of the URL grammar.
func PRLinkScanText(value any) (string, bool) {
	switch value.(type) {
	case map[string]any, []any:
	default:
		return ObservedString(value)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", false
	}
	return strings.TrimSpace(buffer.String()), true
}

// PRLinkURLs returns the distinct pull/merge-request URLs present verbatim in
// text, in first-seen order. A token that parses but is not a pull/merge-request
// permalink (or lacks a host or http(s) scheme) is rejected, so a plain issue or
// docs URL never becomes a false candidate.
func PRLinkURLs(text string) []string {
	seen := map[string]struct{}{}
	var candidates []string
	for _, raw := range prLinkURLPattern.FindAllString(text, -1) {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || !prLinkPathPattern.MatchString(strings.TrimSuffix(parsed.Path, "/")) {
			continue
		}
		if _, found := seen[raw]; found {
			continue
		}
		seen[raw] = struct{}{}
		candidates = append(candidates, raw)
	}
	return candidates
}
