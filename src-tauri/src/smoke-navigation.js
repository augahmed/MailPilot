// Only evaluated by --smoke-test with an isolated unconfigured/synthetic personal
// profile. personalGoogle is a trusted boolean supplied by the native harness.
// Production navigation never mounts features before authorization.
// Wait for React to commit the status response before inspecting the landing UI.
void (async () => {
  for (let attempt = 0; attempt < 50; attempt++) {
    const landing = document.querySelector(".connection-landing");
    if (
      landing?.textContent.includes(
        personalGoogle ? "Gmail disconnected" : "Gmail setup required",
      )
    ) {
      const connect = landing.querySelector(".connect-button");
      if (
        document.querySelector(".feature-grid") ||
        document.querySelector(".mailbox") ||
        !connect ||
        connect.disabled !== !personalGoogle
      )
        throw new Error("Disconnected navigation gate failed");
      const invoke = window.__TAURI_INTERNALS__.invoke;
      const filter = { sender: "", after: 0, before: 0, categoryId: "" };
      const checks = [
        ["mailbox_list", { offset: 0 }],
        [
          "categories_manage",
          {
            request: {
              operation: "list",
              accountId: "",
              offset: 0,
              id: "",
              name: "",
              description: "",
              enabled: null,
            },
          },
        ],
        [
          "classification_review",
          { request: { operation: "list", accountId: "", offset: 0 } },
        ],
        [
          "organization_manage",
          {
            request: {
              operation: "list",
              accountId: "",
              offset: 0,
              emailIds: [],
              actionId: "",
              digest: "",
            },
          },
        ],
        [
          "reply_manage",
          {
            request: {
              operation: "status",
              accountId: "",
              jobId: "",
              actionId: "",
              approved: false,
            },
          },
        ],
        [
          "ai_settings",
          {
            request: {
              operation: "status",
              accountId: "",
              provider: "",
              model: "",
              apiKey: "",
            },
          },
        ],
        ["local_index", { request: { operation: "status", accountId: "" } }],
        [
          "vector_search",
          {
            request: {
              operation: "status",
              accountId: "",
              jobId: "",
              query: "",
              limit: 0,
              filter,
            },
          },
        ],
        [
          "email_answer",
          {
            request: {
              operation: "status",
              accountId: "",
              question: "",
              filter,
              jobId: "",
              previewId: "",
              digest: "",
              sourceIds: [],
              approved: false,
            },
          },
        ],
      ];
      for (const [command, parameters] of checks)
        await invoke(command, parameters);
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("Gmail landing page did not render");
})();
