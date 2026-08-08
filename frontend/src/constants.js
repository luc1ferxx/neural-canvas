export const TOKEN_KEY = "token";
// Overridable so local development can point at a local backend instead of
// production. Only non-secret values belong in REACT_APP_* variables: Create
// React App inlines them into the bundle, where anyone can read them.
export const BASE_URL =
  process.env.REACT_APP_API_BASE_URL ||
  "https://socialai-477705.wl.r.appspot.com";
export const SEARCH_KEY = {
  all: 0,
  keyword: 1,
  user: 2,
};
