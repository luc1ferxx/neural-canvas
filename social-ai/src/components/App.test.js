import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import App from "./App";
import { TOKEN_KEY } from "../constants";

// The previous version of this test lived at src/App.test.js and imported
// "./App", but the component is at src/components/App.js -- so the only
// frontend test failed to resolve its import and never ran. It also asserted on
// Create React App's default "learn react" link, which this app does not render.

/**
 * Builds an unsigned JWT with the given expiry. The app only reads the payload
 * to decide whether to render a logged-in shell; the signature is verified by
 * the backend, so a placeholder is enough here.
 */
const makeToken = (expiresInSeconds) => {
  const b64url = (obj) =>
    btoa(JSON.stringify(obj))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");

  const header = b64url({ alg: "HS256", typ: "JWT" });
  const payload = b64url({
    username: "tester",
    exp: Math.floor(Date.now() / 1000) + expiresInSeconds,
  });
  return `${header}.${payload}.not-a-real-signature`;
};

const renderApp = (initialPath = "/") =>
  render(
    <MemoryRouter initialEntries={[initialPath]}>
      <App />
    </MemoryRouter>,
  );

beforeEach(() => {
  localStorage.clear();
});

describe("App routing and auth gating", () => {
  test("shows the login form when no token is stored", () => {
    renderApp();

    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /log in/i })).toBeInTheDocument();
  });

  test("offers a link to register", () => {
    renderApp();

    expect(
      screen.getByRole("link", { name: /register now/i }),
    ).toBeInTheDocument();
  });

  test("redirects an unauthenticated visit to /create back to login", () => {
    renderApp("/create");

    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
    expect(
      screen.queryByPlaceholderText(/detailed description of the photo/i),
    ).not.toBeInTheDocument();
  });

  test("renders the generation page when a valid token is present", () => {
    localStorage.setItem(TOKEN_KEY, makeToken(3600));

    renderApp("/create");

    expect(
      screen.getByPlaceholderText(/detailed description of the photo/i),
    ).toBeInTheDocument();
  });

  // The app used to treat "a token exists" as "logged in", so an expired token
  // rendered the full UI and every request then failed with a generic error.
  test("treats an expired token as logged out", () => {
    localStorage.setItem(TOKEN_KEY, makeToken(-60));

    renderApp("/create");

    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
  });

  test("discards an expired token from storage on load", () => {
    localStorage.setItem(TOKEN_KEY, makeToken(-60));

    renderApp();

    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
  });

  // A malformed token cannot be trusted either: it has no readable expiry.
  test("treats a malformed token as logged out", () => {
    localStorage.setItem(TOKEN_KEY, "not-a-jwt");

    renderApp("/create");

    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
  });

  test("unknown routes fall back to the root route", () => {
    renderApp("/no-such-page");

    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
  });
});
