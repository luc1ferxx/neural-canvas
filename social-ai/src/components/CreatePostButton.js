import React, { Component } from "react";
import { Modal, Button, message } from "antd";

import { PostForm } from "./PostForm";
import api from "../api";

class CreatePostButton extends Component {
  state = {
    visible: false,
    confirmLoading: false,
  };

  showModal = () => {
    this.setState({
      visible: true,
    });
  };

  handleOk = () => {
    this.setState({
      confirmLoading: true,
    });

    // get form data
    this.postForm
      .validateFields()
      .then((form) => {
        const { description, uploadPost } = form;
        const selected = uploadPost && uploadPost[0];
        if (!selected || !selected.originFileObj) {
          message.error("Please select an image or video.");
          this.setState({ confirmLoading: false });
          return;
        }

        // The browser-reported type can be absent or something other than
        // image/video. Calling .match(...)[0] on it threw a TypeError for, say,
        // a PDF, and because the old code only proceeded when the match
        // succeeded, confirmLoading was never cleared and the modal span
        // forever.
        const browserType = selected.type || "";
        if (!/^(image|video)\//.test(browserType)) {
          message.error("Only image and video files can be uploaded.");
          this.setState({ confirmLoading: false });
          return;
        }

        const formData = new FormData();
        formData.append("message", description);
        formData.append("media_file", selected.originFileObj);

        api
          .post("/upload", formData)
          .then((res) => {
            if (res.status === 200) {
              message.success("The image/video is uploaded!");
              this.postForm.resetFields();
              this.handleCancel();
              // Use the type the backend assigned. It decides by inspecting the
              // file's bytes, so it is authoritative where the browser's
              // Content-Type guess is not.
              const postType =
                (res.data && res.data.type) || browserType.split("/")[0];
              this.props.onShowPost(postType);
            }
          })
          .catch((err) => {
            console.log("Upload image/video failed: ", err.message);
            message.error("Failed to upload image/video!");
          })
          .finally(() => {
            this.setState({ confirmLoading: false });
          });
      })
      .catch((err) => {
        console.log("err validate form -> ", err);
        // Validation failure must also release the button.
        this.setState({ confirmLoading: false });
      });
  };

  handleCancel = () => {
    console.log("Clicked cancel button");
    this.setState({
      visible: false,
    });
  };

  render() {
    const { visible, confirmLoading } = this.state;
    return (
      <div>
        <Button type="primary" onClick={this.showModal}>
          Create New Post
        </Button>
        <Modal
          title="Create New Post"
          open={visible}
          onOk={this.handleOk}
          okText="Create"
          confirmLoading={confirmLoading}
          onCancel={this.handleCancel}
        >
          <PostForm ref={(refInstance) => (this.postForm = refInstance)} />
        </Modal>
      </div>
    );
  }
}
export default CreatePostButton;
