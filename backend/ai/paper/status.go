package paper

// What the card asks of a kept paper: how far it came, to go on from
// there, and the outline the user edited, kept for drafting.

// SaveOutline keeps the outline as the user edited it on the card, for
// drafting. Notes it names that the dossier doesn't have are dropped; it
// needs a section, and every section a title.
func SaveOutline(root, id string, outline Outline) (Outline, error) {
	files, err := Load(root, id)
	if err != nil {
		return Outline{}, err
	}
	edited, err := AsEdited(outline, files.Dossier)
	if err != nil {
		return Outline{}, err
	}
	files.Outline = &edited
	files.Paper.Stage = StageDrafting
	// Sections drafted from an outline before this one aren't this one's.
	if err := ClearSections(root, id); err != nil {
		return Outline{}, err
	}
	if err := Save(root, files); err != nil {
		return Outline{}, err
	}
	return edited, nil
}

// PaperProgress is how far a kept paper has come: what its card offers to go
// on with (PaperProgress in src/platform/bindings/ai-paper.ts).
type PaperProgress struct {
	Paper   Paper    `json:"paper"`
	Outline *Outline `json:"outline"`
	// Drafted is how many of the outline's sections are written.
	Drafted uint32    `json:"drafted"`
	Form    GenreForm `json:"form"`
}

// Status is paper id as it was kept under root, for its card to go on from
// where it stopped.
func Status(root, id string) (PaperProgress, error) {
	files, err := Load(root, id)
	if err != nil {
		return PaperProgress{}, err
	}
	sections := 0
	if files.Outline != nil {
		sections = len(files.Outline.Sections)
	}
	var drafted uint32
	for i := range sections {
		_, ok, err := LoadSection(root, id, i)
		if err != nil {
			return PaperProgress{}, err
		}
		if ok {
			drafted++
		}
	}
	return PaperProgress{Paper: files.Paper, Outline: files.Outline, Drafted: drafted, Form: files.Paper.Genre.Form()}, nil
}
