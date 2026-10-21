package domain

// SemanticEvidence is a new, explicitly versioned assessment contract. Old rule
// hashes and immutable organization journals retain their original meanings.
type SemanticProfile struct {
	ModelID, ModelVersion, Fingerprint, PreprocessingVersion, IndexVersion, CategoryRecipe string
	Dimension                                                                              int
}
type ClassificationChunkEvidence struct {
	ChunkID            string
	Section            string
	Similarity, Weight float64
}
type CategoryScore struct {
	CategoryID      CategoryID
	Score           float64
	PrototypeHash   string
	PrototypeStatus string
	ChunkCount      int
	Evidence        []ClassificationChunkEvidence
}
type SemanticEvidence struct {
	Version                                                            string
	Profile                                                            SemanticProfile
	Revision, EmailRevision, BaseHash, SourceRevision, ChunkGeneration string
	State                                                              string // suggested, assigned, unclassified
	Assigned                                                           []CategoryID
	Scores                                                             []CategoryScore
	MinimumScore, MinimumMargin                                        float64
}
type CategoryPreference struct {
	Mode       string // empty means existing assignments; assigned or unclassified is explicit.
	CategoryID CategoryID
	Revision   string
}
