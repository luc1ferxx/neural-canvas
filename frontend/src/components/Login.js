import React from "react";
import { Form, Input, Button, message } from "antd";
import { UserOutlined, LockOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";

import api, { errorCode, errorMessage } from "../api";

function Login(props) {
  const { handleLoggedIn } = props;

  const onFinish = (values) => {
    const { username, password } = values;

    api
      .post("/signin", { username, password })
      .then((res) => {
        if (res.status === 200) {
          // The token arrives as {"token": "..."}. It used to be the whole
          // response body as text/plain, so passing res.data straight through
          // would now hand the caller an object.
          handleLoggedIn(res.data.token);
          message.success("Login succeeded! ");
        }
      })
      .catch((err) => {
        console.log("login failed: ", err.message);
        // Branch on the backend's stable code where there is one, and fall back
        // to the status for a response that never reached the app.
        const code = errorCode(err);
        const status = err.response && err.response.status;
        if (code === "rate_limited" || status === 429) {
          message.error(
            errorMessage(err, "Too many failed attempts. Please try again later."),
          );
        } else if (code === "unauthorized" || status === 401) {
          message.error("Invalid username or password.");
        } else {
          message.error(errorMessage(err, "Login failed!"));
        }
      });
  };

  return (
    <Form name="normal_login" className="login-form" onFinish={onFinish}>
      <Form.Item
        name="username"
        rules={[
          {
            required: true,
            message: "Please input your Username!",
          },
        ]}
      >
        <Input
          prefix={<UserOutlined className="site-form-item-icon" />}
          placeholder="Username"
        />
      </Form.Item>
      <Form.Item
        name="password"
        rules={[
          {
            required: true,
            message: "Please input your Password!",
          },
        ]}
      >
        <Input
          prefix={<LockOutlined className="site-form-item-icon" />}
          type="password"
          placeholder="Password"
        />
      </Form.Item>

      <Form.Item>
        <Button
          type="primary"
          htmlType="submit"
          className="login-form-button"
          style={{ backgroundColor: "black" }}
        >
          Log in
        </Button>
        Or <Link to="/register">register now!</Link>
      </Form.Item>
    </Form>
  );
}

export default Login;
