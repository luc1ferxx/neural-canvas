import React from "react";
import { useNavigate } from "react-router-dom";
import { Form, Input, Button, message } from "antd";

import api, { errorMessage } from "../api";

// (mobile) responsiveness design
const formItemLayout = {
  labelCol: {
    xs: { span: 24 },
    sm: { span: 8 },
  },
  wrapperCol: {
    xs: { span: 24 },
    sm: { span: 16 },
  },
};

const tailFormItemLayout = {
  wrapperCol: {
    xs: {
      span: 16,
      offset: 0,
    },
    sm: {
      span: 16,
      offset: 8,
    },
  },
};

function Register(props) {
  const [form] = Form.useForm();
  const navigate = useNavigate();

  const onFinish = (values) => {
    const { username, password } = values;

    api
      .post("/signup", { username, password })
      .then((response) => {
        if (response.status === 200) {
          message.success("Registration succeeded!");
          navigate("/login");
        }
      })
      .catch((error) => {
        console.log("register failed: ", error.message);
        // The backend validates the username format, the password length and
        // whether the name is taken, and explains which one failed. Showing
        // only "Registration failed!" hid all of that from the user.
        message.error(errorMessage(error, "Registration failed!"));
      });
  };

  return (
    <Form
      {...formItemLayout}
      form={form}
      name="register"
      onFinish={onFinish}
      className="register"
    >
      <Form.Item
        name="username"
        label="Username"
        rules={[
          {
            required: true,
            message: "Please input your Username!",
          },
        ]}
      >
        <Input />
      </Form.Item>

      <Form.Item
        name="password"
        label="Password"
        rules={[
          {
            required: true,
            message: "Please input your password!",
          },
        ]}
        hasFeedback
      >
        <Input.Password />
      </Form.Item>

      <Form.Item
        name="confirm"
        label="Confirm Password"
        dependencies={["password"]}
        hasFeedback
        rules={[
          {
            required: true,
            message: "Please confirm your password!",
          },
          ({ getFieldValue }) => ({
            validator(rule, value) {
              if (!value || getFieldValue("password") === value) {
                return Promise.resolve();
              }
              return Promise.reject(
                "The two passwords that you entered do not match!"
              );
            },
          }),
        ]}
      >
        <Input.Password />
      </Form.Item>

      <Form.Item {...tailFormItemLayout}>
        <Button
          type="primary"
          htmlType="submit"
          className="register-btn"
          style={{ backgroundColor: "black" }}
        >
          Register
        </Button>
      </Form.Item>
    </Form>
  );
}

export default Register;
