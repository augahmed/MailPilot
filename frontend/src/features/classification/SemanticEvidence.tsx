import type { SemanticReview } from "../../lib/ipc/classification";
export function SemanticEvidence({
  evidence,
  onOpen,
}: {
  evidence: SemanticReview;
  onOpen: (text: string) => void;
}) {
  return (
    <details className="semantic-evidence">
      <summary>Category scores and matching passages</summary>
      <p>Model: {evidence.modelVersion}.</p>
      {evidence.scores.length > 0 && (
        <p>
          These are raw cosine evidence scores, not probabilities. Suggestions
          require score ≥ {evidence.minimumScore.toFixed(2)}. Categories within{" "}
          {evidence.minimumMargin.toFixed(2)} of the best score are suggested
          together.
        </p>
      )}
      {evidence.scores.map((s) => (
        <section
          key={s.categoryId}
          aria-label={`Evidence for ${s.categoryName}`}
        >
          <h4>
            {s.categoryName}: {s.score.toFixed(3)}
          </h4>
          <p>
            {s.chunkCount} distinct passages contributed. Showing{" "}
            {s.evidence.length} strongest weighted passages. Category
            representation: {s.prototypeStatus}.
          </p>
          {s.evidence.map((c) => (
            <div key={c.chunkId}>
              <blockquote>{c.text}</blockquote>
              <small>
                {c.section} · cosine {c.similarity.toFixed(3)} · weight{" "}
                {c.weight.toFixed(3)}
              </small>
              <button onClick={() => onOpen(c.text)}>
                Open matching passage from {s.categoryName}
              </button>
            </div>
          ))}
        </section>
      ))}
      {evidence.scores.length === 0 && (
        <p>
          No category scores were computed. See the abstention reason above.
        </p>
      )}
    </details>
  );
}
