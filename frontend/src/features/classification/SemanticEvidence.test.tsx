import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import fixture from "../../../../contracts/classification-local-v1.json";
import type { SemanticReview } from "../../lib/ipc/classification";
import { SemanticEvidence } from "./SemanticEvidence";

const evidence = fixture.response.rows[0]!.semantic as SemanticReview;

it("shows score thresholds only when category scores were computed", () => {
  const { rerender } = render(
    <SemanticEvidence evidence={evidence} onOpen={() => {}} />,
  );
  expect(screen.getByText(/Suggestions require score/)).toBeInTheDocument();
  rerender(
    <SemanticEvidence
      evidence={{ ...evidence, state: "unclassified", scores: [] }}
      onOpen={() => {}}
    />,
  );
  expect(
    screen.queryByText(/Suggestions require score/),
  ).not.toBeInTheDocument();
  expect(
    screen.getByText(/No category scores were computed/),
  ).toBeInTheDocument();
});
