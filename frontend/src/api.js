import axios from "axios";

import { BASE_URL, TOKEN_KEY } from "./constants";

// AUTH_LOGOUT_EVENT lets the interceptor below tell React that the session is
// gone without api.js needing a reference to component state.
export const AUTH_LOGOUT_EVENT = "auth:logout";

/**
 * Decodes a JWT payload without verifying it.
 *
 * Verification is the server's job -- the signature cannot be checked here, and
 * pretending otherwise would be security theatre. This exists only so the UI can
 * avoid rendering a logged-in shell around a token that is already expired.
 */
const decodeTokenPayload = (token) => {
  try {
    const payload = token.split(".")[1];
    if (!payload) {
      return null;
    }
    // JWT uses base64url; atob expects standard base64.
    const normalized = payload.replace(/-/g, "+").replace(/_/g, "/");
    return JSON.parse(atob(normalized));
  } catch {
    return null;
  }
};

/**
 * Reports whether a stored token is structurally valid and not yet expired.
 *
 * Previously the app treated "a token exists in localStorage" as "logged in", so
 * an expired token rendered the full UI and every request then failed with a
 * generic error toast.
 */
export const isTokenValid = (token) => {
  if (!token) {
    return false;
  }
  const payload = decodeTokenPayload(token);
  if (!payload || typeof payload.exp !== "number") {
    return false;
  }
  // exp is in seconds.
  return payload.exp * 1000 > Date.now();
};

export const getToken = () => localStorage.getItem(TOKEN_KEY);

export const clearToken = () => localStorage.removeItem(TOKEN_KEY);

const api = axios.create({ baseURL: BASE_URL });

// Attach the bearer token in one place instead of repeating the header at every
// call site.
api.interceptors.request.use((config) => {
  const token = getToken();
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

// Endpoints where a 401 means "those credentials are wrong", not "your session
// expired". Treating a failed login as a session logout would be misleading.
const AUTH_ENDPOINTS = ["/signin", "/signup"];

const isAuthEndpoint = (url = "") =>
  AUTH_ENDPOINTS.some((path) => url.startsWith(path));

// A 401 on any other endpoint means the token is gone or expired. Drop it and
// announce the logout so the app returns to the login screen, rather than
// leaving the user on a page where every action fails.
api.interceptors.response.use(
  (response) => response,
  (error) => {
    const status = error.response && error.response.status;
    const url = (error.config && error.config.url) || "";
    if (status === 401 && !isAuthEndpoint(url)) {
      clearToken();
      window.dispatchEvent(new Event(AUTH_LOGOUT_EVENT));
    }
    return Promise.reject(error);
  },
);

export default api;

/**
 * Reads the backend's error envelope: {"error": {code, message, request_id}}.
 *
 * Centralised here because the alternative is every component knowing the wire
 * format. It also has to cope with three other shapes that are not that envelope:
 * a plain string (an older deployment, or an infrastructure error page from a
 * proxy that never reached the app), and nothing at all (the request never got a
 * response, so there is no body to read).
 */
const envelope = (error) => {
  const data = error && error.response && error.response.data;
  if (data && typeof data === "object" && data.error) {
    return data.error;
  }
  return null;
};

/**
 * A stable code to branch on, or "" when the response carried none.
 *
 * Prefer this over matching on the message: the message is prose and may be
 * reworded, whereas the code is part of the contract.
 */
export const errorCode = (error) => {
  const detail = envelope(error);
  return (detail && detail.code) || "";
};

/** The request id, for a user to quote in a bug report. */
export const errorRequestID = (error) => {
  const detail = envelope(error);
  return (detail && detail.request_id) || "";
};

/**
 * The message to show the user, falling back when the server did not supply one.
 */
export const errorMessage = (error, fallback) => {
  const detail = envelope(error);
  if (detail && detail.message) {
    return detail.message;
  }
  // A pre-envelope deployment, or a proxy error page.
  const data = error && error.response && error.response.data;
  if (typeof data === "string" && data.trim()) {
    return data.trim();
  }
  return fallback;
};
