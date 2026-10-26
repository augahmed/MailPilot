// Synthetic configuration only. Never replace these canaries with a real client.
export const personalFixture = {
  installed: {
    client_id: "synthetic-personal.apps.googleusercontent.com",
    client_secret: "synthetic-personal-client-canary",
    project_id: "synthetic-project-not-packaged",
    auth_uri: "https://accounts.google.com/o/oauth2/auth",
    token_uri: "https://oauth2.googleapis.com/token",
    auth_provider_x509_cert_url: "https://www.googleapis.com/oauth2/v1/certs",
    redirect_uris: ["http://localhost"],
  },
};
