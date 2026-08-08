import MockAdapter from "axios-mock-adapter";

import api, {
  AUTH_LOGOUT_EVENT,
  errorCode,
  errorMessage,
  errorRequestID,
  isTokenValid,
} from "./api";
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

// The backend answers every failure with {"error": {code, message, request_id}}.
// It used to answer with text/plain prose, so these helpers have to keep working
// against a deployment that has not been updated yet, and against a response that
// never reached the app at all.
describe("error envelope helpers", () => {
  const enveloped = (code, msg, requestID) => ({
    response: {
      data: { error: { code, message: msg, request_id: requestID } },
    },
  });

  test("reads the code, message and request id", () => {
    const err = enveloped("rate_limited", "Slow down", "abc123");
    expect(errorCode(err)).toBe("rate_limited");
    expect(errorMessage(err, "fallback")).toBe("Slow down");
    expect(errorRequestID(err)).toBe("abc123");
  });

  test("falls back to the caller's message when the envelope has none", () => {
    const err = { response: { data: { error: { code: "internal" } } } };
    expect(errorMessage(err, "fallback")).toBe("fallback");
    expect(errorCode(err)).toBe("internal");
  });

  // A pre-envelope deployment, or an error page from a proxy that never reached
  // the app. Neither is an envelope, and neither should produce "[object Object]".
  test("accepts a plain string body", () => {
    const err = { response: { data: "  Something broke  " } };
    expect(errorMessage(err, "fallback")).toBe("Something broke");
    expect(errorCode(err)).toBe("");
  });

  test("survives a response with no body", () => {
    expect(errorMessage({ response: {} }, "fallback")).toBe("fallback");
    expect(errorCode({ response: {} })).toBe("");
    expect(errorRequestID({ response: {} })).toBe("");
  });

  // A network failure or a timeout: there is no response object at all.
  test("survives an error with no response", () => {
    expect(errorMessage({ message: "Network Error" }, "fallback")).toBe("fallback");
    expect(errorCode({})).toBe("");
    expect(errorMessage(undefined, "fallback")).toBe("fallback");
    expect(errorRequestID(null)).toBe("");
  });

  test("ignores an empty string body", () => {
    expect(errorMessage({ response: { data: "   " } }, "fallback")).toBe("fallback");
  });
});

describe("signin response shape", () => {
  let mock;
  beforeEach(() => {
    mock = new MockAdapter(api);
  });
  afterEach(() => {
    mock.restore();
    localStorage.clear();
  });

  // The token moved from a text/parseable body into {"token": ...}. Pinning it
  // here because the failure mode is silent: handleLoggedIn would store an object,
  // and the app would look logged in until the next request went out unauthorized.
  test("the token arrives under a token key", async () => {
    mock.onPost("/signin").reply(200, { token: "a.b.c" });

    const res = await api.post("/signin", { username: "u", password: "p" });
    expect(res.data.token).toBe("a.b.c");
    expect(typeof res.data).toBe("object");
  });
});
