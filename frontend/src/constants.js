export const TOKEN_KEY = "token";
// Overridable so a deployment can point at its own backend. Only non-secret
// values belong in REACT_APP_* variables: Create React App inlines them into the
// bundle at build time, where anyone can read them -- and changing one means
// rebuilding, not restarting.
//
// The default is local, so a fresh clone works with `docker compose up` and
// nothing else. A deployed frontend has to set this at build time.
export const BASE_URL =
  process.env.REACT_APP_API_BASE_URL || "http://localhost:8080";
export const SEARCH_KEY = {
  all: 0,
  keyword: 1,
  user: 2,
};
