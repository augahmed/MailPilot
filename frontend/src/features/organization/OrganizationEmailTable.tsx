import type { OrganizationResponse } from "../../lib/ipc/client";
import { classificationLabel, organizationBatchSize } from "./listEmails";
import {
  OrganizationSuggestionPicker,
  type SuggestionChoice,
} from "./OrganizationSuggestionPicker";

type Row = OrganizationResponse["rows"][number];
export function OrganizationEmailTable({
  rows,
  selected = [],
  onSelect,
  disabled = false,
  undo = false,
  accountId = "",
  choices = {},
  onChoice,
  onUnauthorized = () => {},
}: {
  rows: Row[];
  selected?: string[];
  onSelect?: (ids: string[]) => void;
  disabled?: boolean;
  undo?: boolean;
  accountId?: string;
  choices?: Record<string, SuggestionChoice>;
  onChoice?: (emailId: string, value?: SuggestionChoice) => void;
  onUnauthorized?: () => void;
}) {
  const selectable = !!onSelect;
  return (
    <div
      className="mail-table"
      role="region"
      aria-label={
        selectable ? "Emails to organize" : "Selected Gmail label changes"
      }
      tabIndex={0}
    >
      <table
        className={
          selectable
            ? "organization-selection-table"
            : "organization-preview-table"
        }
      >
        <caption>
          {selectable
            ? "Cached emails"
            : undo
              ? "Gmail labels to undo"
              : "Previewed Gmail label changes"}
        </caption>
        <thead>
          <tr>
            {selectable && <th scope="col">Select</th>}
            <th scope="col">Email</th>
            {selectable && <th scope="col">Category</th>}
            <th scope="col">Current Gmail labels</th>
            <th scope="col">
              {selectable ? "Proposed Gmail label" : "Change to confirm"}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.emailId}>
              {selectable && (
                <td>
                  {(row.change === "candidate" || !!row.suggestionCount) && (
                    <input
                      type="checkbox"
                      aria-label={`Select ${row.subject || "email"}`}
                      checked={selected.includes(row.emailId)}
                      disabled={
                        disabled ||
                        (row.change === "multiple_suggestions" &&
                          !choices[row.emailId]) ||
                        (!selected.includes(row.emailId) &&
                          selected.length >= organizationBatchSize)
                      }
                      onChange={(event) =>
                        onSelect(
                          event.target.checked
                            ? [...selected, row.emailId].slice(
                                0,
                                organizationBatchSize,
                              )
                            : selected.filter((id) => id !== row.emailId),
                        )
                      }
                    />
                  )}
                </td>
              )}
              <td>
                <strong>{row.subject || "(No subject)"}</strong>
                <small>{row.sender}</small>
                {(row.phishing ||
                  row.confidence > 0 ||
                  row.change === "multiple_suggestions") && (
                  <details className="email-evidence">
                    <summary>Classification evidence</summary>
                    {row.phishing ? (
                      <>
                        <p>
                          High phishing risk · {row.phishing.score}/100
                          (threshold {row.phishing.threshold}). This is not a
                          guarantee of safety.
                        </p>
                        <ul>
                          {row.phishing.reasons.map((reason) => (
                            <li key={reason.code}>
                              {reason.points > 0 ? "+" : ""}
                              {reason.points} {reason.explanation}
                            </li>
                          ))}
                        </ul>
                      </>
                    ) : row.change === "multiple_suggestions" ? (
                      <p>
                        {row.suggestionCount
                          ? "Choose among the automatic suggestions for this batch. This does not assign a manual category."
                          : "This email has multiple existing manual assignments. Choose a primary category in Categories & Classification to resolve them before applying a Gmail label."}
                      </p>
                    ) : (
                      <p>
                        {row.scoreKind === "user_assignment"
                          ? "User assigned"
                          : `Automatic suggestion · ${row.scoreKind === "cosine_similarity" ? "Cosine score" : row.scoreKind === "rule_confidence" ? "Rule strength" : "Evidence score"} ${row.confidence.toFixed(3)}`}
                      </p>
                    )}
                  </details>
                )}
              </td>
              {selectable && (
                <td>
                  {row.phishing
                    ? "Potential Phishing"
                    : classificationLabel(row)}
                  {!!row.suggestionCount &&
                    row.change === "multiple_suggestions" &&
                    onChoice && (
                      <OrganizationSuggestionPicker
                        accountId={accountId}
                        row={row}
                        value={choices[row.emailId]}
                        disabled={disabled}
                        onChange={(value) => onChoice(row.emailId, value)}
                        onUnauthorized={onUnauthorized}
                      />
                    )}
                </td>
              )}
              <td>{row.currentLabels.join(", ") || "No labels"}</td>
              <td>
                {!selectable
                  ? row.change === "none"
                    ? `Already applied: ${row.proposedLabel}`
                    : `${undo ? "Remove" : "Add"}: ${row.proposedLabel}`
                  : (choices[row.emailId]
                      ? `MailPilot/${choices[row.emailId]!.name}`
                      : row.proposedLabel) ||
                    (row.change === "multiple_suggestions"
                      ? row.suggestionCount
                        ? "Choose a suggestion for this batch"
                        : "Choose a primary category first"
                      : row.change === "below_threshold"
                        ? "No HIGH-risk label suggested"
                        : row.change === "risk_unavailable"
                          ? "Risk analysis unavailable"
                          : "Choose a category first")}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
