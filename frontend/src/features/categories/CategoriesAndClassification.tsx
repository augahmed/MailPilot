import { Categories } from "./Categories";
import { ClassificationReview } from "../classification/ClassificationReview";

export function CategoriesAndClassification() {
  return (
    <div className="categories-workspace">
      <nav
        className="section-links"
        aria-label="Categories and classification sections"
      >
        <a href="#categories-heading">Categories</a>
        <a href="#classification-heading">Classification Review</a>
      </nav>
      <Categories />
      <ClassificationReview />
    </div>
  );
}
