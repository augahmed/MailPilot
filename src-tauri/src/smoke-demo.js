// Native demo qualification only. Never run in a real-account session.
void (async () => {
  const wait = async (predicate) => {
    for (let i = 0; i < 150; i++) {
      if (predicate()) return;
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    throw new Error("Synthetic demo screen did not render");
  };
  const button = (text) =>
    [...document.querySelectorAll("button")].find((b) =>
      b.textContent.includes(text),
    );
  await wait(
    () =>
      document.querySelector(".demo-banner") &&
      document
        .querySelector(".mailbox")
        ?.textContent.includes("Application received"),
  );
  if (
    !button("Offline demo")?.disabled ||
    document.querySelector(".connection-landing")
  )
    throw new Error("Demo authorization presentation failed");
  button("Email Search").click();
  await wait(
    () =>
      document.querySelector(".demo-banner") &&
      document.body.textContent.includes("Search"),
  );
  button("Back to dashboard").click();
  button("Ask About Email").click();
  await wait(() =>
    document.body.textContent.includes(
      "External services are disabled in this demo",
    ),
  );
  if (document.querySelector("input[type=password]"))
    throw new Error("Demo exposed a key entry flow");
  const result = await window.__TAURI_INTERNALS__.invoke("gmail_connect");
  if (!result.demo || result.code !== "demo_mode")
    throw new Error("Demo offered Google authorization");
  await window.__TAURI_INTERNALS__.invoke("gmail_check");
})();
