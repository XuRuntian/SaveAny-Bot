package re

import "regexp"

var (
	TgMessageLinkRegexString = `https?://(?:t|telegram)\.me/(?:c/\d+|[A-Za-z0-9_]+)/\d+(?:/\d+)?(?:\?[^\s#]*[A-Za-z0-9_])?\b`
	TgMessageLinkRegexp      = regexp.MustCompile(TgMessageLinkRegexString)
	tgMessageLinkStartRegexp = regexp.MustCompile(`https?://(?:t|telegram)\.me/`)
	TelegraphUrlRegexString  = `https://telegra\.ph/[^\s]+`
	TelegraphUrlRegexp       = regexp.MustCompile(TelegraphUrlRegexString)
)

func ExtractTgMessageLinks(text string) []string {
	starts := tgMessageLinkStartRegexp.FindAllStringIndex(text, -1)
	if len(starts) == 0 {
		return nil
	}
	links := make([]string, 0, len(starts))
	for i, start := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		if match := TgMessageLinkRegexp.FindString(text[start[0]:end]); match != "" {
			links = append(links, match)
		}
	}
	return links
}
