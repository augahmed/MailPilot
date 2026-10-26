# MailPilot documentation

Start with the guides below. They describe the current local-first desktop app;
historical reports and future implementation prompts do not authorize features.

## Start here

| Read                                    | Purpose                                                                                |
| --------------------------------------- | -------------------------------------------------------------------------------------- |
| [Project README](../README.md)          | Prerequisites, development, desktop builds and the feature overview                    |
| [Gmail setup](GMAIL_SETUP.md)           | External Desktop JSON, personal macOS builds, test-account sign-in and troubleshooting |
| [Architecture](ARCHITECTURE.md)         | Go/React/Rust ownership, shared local pipeline and trust boundaries                    |
| [Privacy and local storage](PRIVACY.md) | Credential handling, unencrypted cache, external AI and retained data                  |
| [Security policy](../SECURITY.md)       | Threat model, reporting route, platform support and known limits                       |
| [Evaluation](EVALUATION.md)             | Dated synthetic quality/security results and unresolved findings                       |

External AI is optional. The [AI contract](GENERATIVE_AI.md) describes the existing
in-app provider/key settings and per-request consent; OpenAI is the only shipped
functional adapter. [Gmail setup](GMAIL_SETUP.md) includes the personal credential
helpers and existing in-app OpenAI settings. A credential-free synthetic demo mode
is not implemented yet. Installed native inference requires no user model download.

## Technical references

- Scope and engineering: [product plan](PRODUCT_PLAN.md),
  [engineering contract](../AGENTS.md),
  [vector implementation design](VECTOR_ARCHITECTURE_PLAN.md),
  [typed IPC and fixtures](../contracts/README.md).
- Mail and lifecycle: [readable text](CLEAN_TEXT.md),
  [Gmail synchronization](SYNC_LIFECYCLE.md),
  [local indexing](LOCAL_INDEXING.md).
- Local intelligence: [native embedding and packaging](LOCAL_EMBEDDING.md),
  [semantic search](VECTOR_SEARCH.md), [keyword/hybrid search](HYBRID_SEARCH.md),
  [category vectors](CATEGORY_VECTORS.md),
  [semantic classification](SEMANTIC_CLASSIFICATION.md),
  [phishing explanations](PHISHING_FEATURES.md).
- External AI and recovery: [provider/consent boundaries](GENERATIVE_AI.md),
  [approved evidence questions](EMAIL_QUESTIONS.md),
  [historical draft recovery only](REPLY_DRAFTING.md).
- Dated evidence: [performance measurements](PERFORMANCE.md),
  [UI review through 2026-10-22](UI_REVIEW.md). Read each report's date, fixtures
  and limitations; historical test counts are not current release certification.

## History and future work

[The archive](archive/README.md) holds superseded designs and earlier review
evidence. Retired Ask/search/draft APIs remain retired even when a historical
fixture or report describes them.

[The portfolio prompt plan](PORTFOLIO_PROMPT_PLAN.md) is an engineering task
sequence, not an implemented feature inventory. Its later prompts require their
own explicit execution request. Public distribution verification, encrypted
storage, cache-removal controls and the visual README work remain separate.
