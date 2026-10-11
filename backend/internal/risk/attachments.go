package risk

import (
	"path"
	"strings"

	"mailpilot.local/core/internal/domain"
)

var executableExtensions = map[string]bool{
	".exe": true, ".scr": true, ".com": true, ".bat": true, ".cmd": true, ".msi": true, ".msp": true, ".pif": true, ".ps1": true, ".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".wsf": true, ".wsh": true, ".hta": true, ".cpl": true, ".dll": true, ".jar": true, ".lnk": true, ".app": true, ".dmg": true, ".pkg": true, ".sh": true, ".bash": true, ".command": true, ".desktop": true, ".py": true, ".pl": true, ".rb": true, ".reg": true,
}

func (x *featureExtraction) attachments(attachments []domain.AttachmentMetadata) {
	if len(attachments) > maxAttachments {
		x.issue("attachment_count_limit", "attachments", maxAttachments)
	}
	for index, a := range attachments[:min(len(attachments), maxAttachments)] {
		if x.ctx.Err() != nil {
			return
		}
		if len(a.Filename) > 1024 {
			x.issue("input_limit", "attachment_filename", index)
			continue
		}
		filename := x.text(a.Filename, "attachment_filename", 1024)
		// Inspect the real suffix after both platform separators and Windows suffix
		// normalization. A MIME type alone cannot establish executable content.
		base := path.Base(strings.ReplaceAll(filename, "\\", "/"))
		base = strings.TrimRight(base, " .")
		extension := strings.ToLower(path.Ext(base))
		if !executableExtensions[extension] {
			continue
		}
		x.f.ExecutableAttachments = append(x.f.ExecutableAttachments, domain.PhishingExecutableAttachment{AttachmentIndex: index, Filename: filename, Extension: extension, DoubleExtension: path.Ext(strings.TrimSuffix(base, path.Ext(base))) != ""})
	}
}
