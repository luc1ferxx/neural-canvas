import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import App from "./App";
import { TOKEN_KEY } from "../constants";

// The previous version of this test lived at src/App.test.js and imported
// "./App", but the component is at src/components/App.js -- so the only
// frontend test failed to resolve its import and never ran. It also asserted on
// Create React App's default "learn react" link, which this app does not render.

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

    expect(screen.getByRole("link", { name: /register now/i })).toBeInTheDocument();
  });

  test("redirects an unauthenticated visit to /create back to login", () => {
    renderApp("/create");

    // The generation page is gated, so the login form is what renders.
    expect(screen.getByPlaceholderText("Username")).toBeInTheDocument();
    expect(
      screen.queryByPlaceholderText(/detailed description of the photo/i),
    ).not.toBeInTheDocument();
  });

  test("renders the generation page when a token is present", () => {
    localStorage.setItem(TOKEN_KEY, "a.fake.token");

    renderApp("/create");

    expect(
      screen.getByPlaceholderText(/detailed description of the photo/i),
    ).toBeInTheDocument();
  });
});
