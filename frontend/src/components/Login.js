import React from "react";
import { Form, Input, Button, message } from "antd";
import { UserOutlined, LockOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";

import api from "../api";

function Login(props) {
  const { handleLoggedIn } = props;

  const onFinish = (values) => {
    const { username, password } = values;

    api
      .post("/signin", { username, password })
      .then((res) => {
        if (res.status === 200) {
          handleLoggedIn(res.data);
          message.success("Login succeeded! ");
        }
      })
      .catch((err) => {
        console.log("login failed: ", err.message);
        // 401 is a wrong username or password; 429 is the sign-in throttle.
        const status = err.response && err.response.status;
        if (status === 429) {
          message.error("Too many failed attempts. Please try again later.");
        } else if (status === 401) {
          message.error("Invalid username or password.");
        } else {
          message.error("Login failed!");
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
