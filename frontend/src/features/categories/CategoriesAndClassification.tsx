import { useState } from "react";
import { Categories } from "./Categories";
import { ClassificationReview } from "../classification/ClassificationReview";

export function CategoriesAndClassification({
  onUpdateClassification,
  reviewRevision = 0,
}: {
  onUpdateClassification?: () => void;
  reviewRevision?: number;
}) {
  const [categoryRevision, setCategoryRevision] = useState(0);
  return (
    <div className="categories-workspace">
      <nav
        className="section-links"
        aria-label="Categories and classification sections"
      >
        <a href="#categories-heading">Categories</a>
        <a href="#classification-heading">Classification Review</a>
      </nav>
      <Categories
        onChanged={() => setCategoryRevision((value) => value + 1)}
        onUpdateClassification={onUpdateClassification}
      />
      <ClassificationReview
        refreshRevision={reviewRevision + categoryRevision}
      />
    </div>
  );
}
