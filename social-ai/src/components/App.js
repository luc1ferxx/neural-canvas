import React, { useState, useEffect } from "react";
import ResponsiveAppBar from "./ResponsiveAppBar";
import Main from "./Main";

import { TOKEN_KEY } from "../constants";
import { AUTH_LOGOUT_EVENT, clearToken, getToken, isTokenValid } from "../api";

function App() {
  // Presence of a token is not enough: an expired one used to render the whole
  // logged-in UI, where every subsequent request failed with a generic error.
  const [isLoggedIn, setIsLoggedIn] = useState(() => isTokenValid(getToken()));

  const logout = () => {
    clearToken();
    setIsLoggedIn(false);
  };

  const loggedIn = (token) => {
    if (token) {
      localStorage.setItem(TOKEN_KEY, token);
      setIsLoggedIn(true);
    }
  };

  useEffect(() => {
    // Discard a token that is present but already expired, so the app starts in
    // a consistent state rather than waiting for the first failed request.
    if (getToken() && !isTokenValid(getToken())) {
      clearToken();
      setIsLoggedIn(false);
    }

    // The API layer emits this when any request comes back 401.
    const handleAuthLogout = () => setIsLoggedIn(false);
    window.addEventListener(AUTH_LOGOUT_EVENT, handleAuthLogout);
    return () =>
      window.removeEventListener(AUTH_LOGOUT_EVENT, handleAuthLogout);
  }, []);

  return (
    <div className="App">
      <ResponsiveAppBar isLoggedIn={isLoggedIn} handleLogout={logout} />
      <Main isLoggedIn={isLoggedIn} handleLoggedIn={loggedIn} />
    </div>
  );
}

export default App;
