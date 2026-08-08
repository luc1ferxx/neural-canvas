import React from "react";
import { Routes, Route, Navigate } from "react-router-dom";

import Login from "./Login";
import Register from "./Register";
import Collection from "./Collection";
import Landing from "./Landing";

function Main(props) {
  const { isLoggedIn, handleLoggedIn } = props;

  // auth gating

  const showLogin = () => {
    return isLoggedIn ? (
      <Navigate to="/create" />
    ) : (
      <Login handleLoggedIn={handleLoggedIn} />
    );
  };

  const showRegister = () => {
    return isLoggedIn ? <Navigate to="/create" /> : <Register />;
  };

  const showLanding = () => {
    return isLoggedIn ? <Landing /> : <Navigate to="/login" />;
  };

  const showCollection = () => {
    return isLoggedIn ? <Collection /> : <Navigate to="/login" />;
  };

  return (
    <div className="main">
      <Routes>
        {/* `exact` was a React Router v5 prop and did nothing in v6. */}
        <Route path="/" element={showLogin()} />
        <Route path="/login" element={showLogin()} />
        <Route path="/register" element={showRegister()} />
        <Route path="/create" element={showLanding()} />
        <Route path="/collection" element={showCollection()} />
        {/* Without a catch-all an unknown URL rendered an empty page. */}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </div>
  );
}

export default Main;
