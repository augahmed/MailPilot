import type { ReactNode } from "react";

export const features = [
  {
    id: "index",
    title: "Local Email Index",
    description: "Prepare mail for local intelligence",
    icon: "index",
  },
  {
    id: "search",
    title: "Email Search",
    description: "Find words, meaning, and context",
    icon: "search",
  },
  {
    id: "ask",
    title: "Ask About Email",
    description: "Answers from evidence you approve",
    icon: "ask",
  },
  {
    id: "categories",
    title: "Categories & Classification",
    description: "Make sense of your cached mail",
    icon: "categories",
  },
  {
    id: "organization",
    title: "Gmail Organization",
    description: "Review and apply label changes",
    icon: "organization",
  },
] as const;
export type Page =
  | "dashboard"
  | "settings"
  | "privacy"
  | "recovery"
  | (typeof features)[number]["id"];

export function Icon({ name }: { name: string }) {
  const paths: Record<string, ReactNode> = {
    mail: (
      <>
        <rect x="3" y="5" width="18" height="14" rx="3" />
        <path d="m3 7 9 6 9-6" />
      </>
    ),
    index: (
      <>
        <rect x="4" y="3" width="16" height="18" rx="2" />
        <path d="M8 8h8M8 12h8M8 16h4" />
      </>
    ),
    search: (
      <>
        <circle cx="10.5" cy="10.5" r="6.5" />
        <path d="m16 16 5 5" />
      </>
    ),
    ask: (
      <>
        <path d="M21 11a8 8 0 0 1-8 8H7l-4 3V11a8 8 0 0 1 8-8h2a8 8 0 0 1 8 8Z" />
        <path d="M9 9a3 3 0 0 1 6 0c0 2-3 2-3 4m0 3h.01" />
      </>
    ),
    categories: (
      <>
        <rect x="3" y="3" width="7" height="7" rx="2" />
        <rect x="14" y="3" width="7" height="7" rx="2" />
        <rect x="3" y="14" width="7" height="7" rx="2" />
        <rect x="14" y="14" width="7" height="7" rx="2" />
      </>
    ),
    organization: (
      <>
        <path d="M3 4h8l10 10-7 7L3 10Z" />
        <circle cx="7.5" cy="8" r="1" />
      </>
    ),
  };
  return (
    <svg
      width="24"
      height="24"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name] ?? paths.mail}
    </svg>
  );
}

export function Brand() {
  return (
    <span className="brand">
      <span className="brand-mark">
        <Icon name="mail" />
      </span>
      <span>MailPilot</span>
    </span>
  );
}
