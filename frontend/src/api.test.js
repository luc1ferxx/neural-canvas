import MockAdapter from "axios-mock-adapter";

import api, { AUTH_LOGOUT_EVENT, isTokenValid } from "./api";
import { TOKEN_KEY } from "./constants";

const makeToken = (payload) => {
  const b64url = (obj) =>
    btoa(JSON.stringify(obj))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
  return `${b64url({ alg: "HS256", typ: "JWT" })}.${b64url(payload)}.sig`;
};

const futureExp = () => Math.floor(Date.now() / 1000) + 3600;
const pastExp = () => Math.floor(Date.now() / 1000) - 3600;

describe("isTokenValid", () => {
  test("accepts a token whose exp is in the future", () => {
    expect(isTokenValid(makeToken({ exp: futureExp() }))).toBe(true);
  });

  test("rejects an expired token", () => {
    expect(isTokenValid(makeToken({ exp: pastExp() }))).toBe(false);
  });

  test("rejects a token with no exp claim", () => {
    expect(isTokenValid(makeToken({ username: "tester" }))).toBe(false);
  });

  test("rejects a token whose exp is not a number", () => {
    expect(isTokenValid(makeToken({ exp: "soon" }))).toBe(false);
  });

  test.each([null, undefined, "", "not-a-jwt", "a.b", "a.!!!.c"])(
    "rejects malformed input %p without throwing",
    (value) => {
      expect(isTokenValid(value)).toBe(false);
    },
  );
});

describe("api interceptors", () => {
  let mock;

  beforeEach(() => {
    mock = new MockAdapter(api);
    localStorage.clear();
  });

  afterEach(() => {
    mock.restore();
  });

  test("attaches the bearer token when one is stored", async () => {
    localStorage.setItem(TOKEN_KEY, "stored-token");
    mock.onGet("/search").reply(200, []);

    await api.get("/search");

    expect(mock.history.get[0].headers.Authorization).toBe(
      "Bearer stored-token",
    );
  });

  test("sends no Authorization header when no token is stored", async () => {
    mock.onGet("/search").reply(200, []);

    await api.get("/search");

    expect(mock.history.get[0].headers.Authorization).toBeUndefined();
  });

  test("a 401 clears the token and announces a logout", async () => {
    localStorage.setItem(TOKEN_KEY, "stale-token");
    mock.onGet("/search").reply(401);

    const onLogout = jest.fn();
    window.addEventListener(AUTH_LOGOUT_EVENT, onLogout);

    await expect(api.get("/search")).rejects.toThrow();

    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(onLogout).toHaveBeenCalledTimes(1);

    window.removeEventListener(AUTH_LOGOUT_EVENT, onLogout);
  });

  // A wrong password is a 401 too, but it is not a session expiry -- reporting
  // it as a logout would be misleading, and there is no session to end.
  test("a 401 from /signin does not announce a logout", async () => {
    mock.onPost("/signin").reply(401);

    const onLogout = jest.fn();
    window.addEventListener(AUTH_LOGOUT_EVENT, onLogout);

    await expect(api.post("/signin", {})).rejects.toThrow();

    expect(onLogout).not.toHaveBeenCalled();

    window.removeEventListener(AUTH_LOGOUT_EVENT, onLogout);
  });

  test("non-401 errors are passed through untouched", async () => {
    localStorage.setItem(TOKEN_KEY, "good-token");
    mock.onPost("/generate").reply(502);

    const onLogout = jest.fn();
    window.addEventListener(AUTH_LOGOUT_EVENT, onLogout);

    await expect(api.post("/generate", {})).rejects.toThrow();

    expect(localStorage.getItem(TOKEN_KEY)).toBe("good-token");
    expect(onLogout).not.toHaveBeenCalled();

    window.removeEventListener(AUTH_LOGOUT_EVENT, onLogout);
  });
});
