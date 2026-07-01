package video

// ConfigOption is one selectable value in a video-metadata taxonomy (category,
// license, language, privacy): a stable id and a human-readable label. It backs
// the GET /api/v1/videos/config endpoint the frontend reads to populate its
// metadata dropdowns, and (later) the create/update validation of those fields.
//
// Ids are stable identifiers, not display text: categories and licenses use
// PeerTube's numeric ids (as strings) so an imported PeerTube video's
// category/licence maps across cleanly; languages use ISO 639-1 codes; privacies
// use Vidra's own `privacy` field values.
type ConfigOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Categories are the video subject categories. Ids match PeerTube's category
// constants for import compatibility. Ordered by id.
var Categories = []ConfigOption{
	{"1", "Music"},
	{"2", "Films"},
	{"3", "Vehicles"},
	{"4", "Art"},
	{"5", "Sports"},
	{"6", "Travels"},
	{"7", "Gaming"},
	{"8", "People"},
	{"9", "Comedy"},
	{"10", "Entertainment"},
	{"11", "News & Politics"},
	{"12", "How To"},
	{"13", "Education"},
	{"14", "Activism"},
	{"15", "Science & Technology"},
	{"16", "Animals"},
	{"17", "Kids"},
	{"18", "Food"},
}

// Licenses are the selectable content licenses. Ids match PeerTube's licence
// constants (Creative Commons + public domain). Ordered by id.
var Licenses = []ConfigOption{
	{"1", "Attribution (CC BY)"},
	{"2", "Attribution - ShareAlike (CC BY-SA)"},
	{"3", "Attribution - NoDerivs (CC BY-ND)"},
	{"4", "Attribution - NonCommercial (CC BY-NC)"},
	{"5", "Attribution - NonCommercial - ShareAlike (CC BY-NC-SA)"},
	{"6", "Attribution - NonCommercial - NoDerivs (CC BY-NC-ND)"},
	{"7", "Public Domain Dedication (CC0)"},
}

// Languages is a curated set of common content languages (ISO 639-1 codes),
// ordered by label. It is intentionally NOT the full ISO 639 list (an
// INTENTIONAL_DIFFERENCE from PeerTube); extend as demand warrants.
var Languages = []ConfigOption{
	{"ar", "Arabic"},
	{"bn", "Bengali"},
	{"zh", "Chinese"},
	{"cs", "Czech"},
	{"da", "Danish"},
	{"nl", "Dutch"},
	{"en", "English"},
	{"fi", "Finnish"},
	{"fr", "French"},
	{"de", "German"},
	{"el", "Greek"},
	{"he", "Hebrew"},
	{"hi", "Hindi"},
	{"hu", "Hungarian"},
	{"id", "Indonesian"},
	{"it", "Italian"},
	{"ja", "Japanese"},
	{"ko", "Korean"},
	{"no", "Norwegian"},
	{"fa", "Persian"},
	{"pl", "Polish"},
	{"pt", "Portuguese"},
	{"ro", "Romanian"},
	{"ru", "Russian"},
	{"es", "Spanish"},
	{"sv", "Swedish"},
	{"th", "Thai"},
	{"tr", "Turkish"},
	{"uk", "Ukrainian"},
	{"vi", "Vietnamese"},
}

// Privacies mirror the video `privacy` field values, in ascending visibility
// order. Ids are the values persisted on a video, not numeric codes.
var Privacies = []ConfigOption{
	{"public", "Public"},
	{"unlisted", "Unlisted"},
	{"private", "Private"},
}

// hasOption reports whether id is one of the options.
func hasOption(opts []ConfigOption, id string) bool {
	for _, o := range opts {
		if o.ID == id {
			return true
		}
	}
	return false
}

// IsCategory / IsLicense / IsLanguage report whether an id is a known taxonomy
// value. They are the source of truth for validating video-metadata input.
func IsCategory(id string) bool { return hasOption(Categories, id) }
func IsLicense(id string) bool  { return hasOption(Licenses, id) }
func IsLanguage(id string) bool { return hasOption(Languages, id) }
