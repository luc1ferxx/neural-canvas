import React, { useState, useEffect } from "react";
import { Tabs, message, Row, Col } from "antd";

import SearchBar from "./SearchBar";
import { SEARCH_KEY } from "../constants";
import api from "../api";
import PhotoGallery from "./PhotoGallery";
import CreatePostButton from "./CreatePostButton";

const { TabPane } = Tabs;

// Matches the backend's default page size.
const PAGE_SIZE = 50;

function Collection(props) {
  const [posts, setPosts] = useState([]);
  const [activeTab, setActiveTab] = useState("image");
  const [searchOption, setSearchOption] = useState({
    type: SEARCH_KEY.all,
    keyword: "",
  });

  const handleSearch = (option) => setSearchOption(option);

  // Refetch whenever the query or the tab changes: the type filter is applied by
  // Elasticsearch now, so each tab is its own request. Previously one request
  // was split between tabs in the browser, which meant a tab could report
  // "No images!" while images existed further down the result set.
  useEffect(() => {
    let ignore = false;

    const { type, keyword } = searchOption;
    const params = new URLSearchParams({
      size: String(PAGE_SIZE),
      type: activeTab,
    });

    if (type === SEARCH_KEY.user) {
      // URLSearchParams encodes the value, so keywords containing & or # no
      // longer truncate the query.
      params.set("user", keyword);
    } else if (type !== SEARCH_KEY.all) {
      params.set("keywords", keyword);
    }

    api
      .get(`/search?${params.toString()}`)
      .then((res) => {
        // Drop the response if the tab or query changed while it was in flight,
        // so a slow earlier request cannot overwrite a newer one.
        if (ignore) {
          return;
        }
        if (res.status === 200) {
          setPosts(res.data);
        }
      })
      .catch((err) => {
        if (ignore) {
          return;
        }
        message.error("Fetch posts failed!");
        console.log("fetch posts failed: ", err.message);
      });

    return () => {
      ignore = true;
    };
  }, [searchOption, activeTab]);

  const renderImages = () => {
    if (!posts || posts.length === 0) {
      return <div>No images!</div>;
    }

    const imageArr = posts.map((image) => ({
      postId: image.id,
      src: image.url,
      user: image.user,
      caption: image.message,
      thumbnail: image.url,
      thumbnailWidth: 300,
      thumbnailHeight: 200,
    }));

    return <PhotoGallery images={imageArr} />;
  };

  const renderVideos = () => {
    if (!posts || posts.length === 0) {
      return <div>No videos!</div>;
    }

    return (
      <Row>
        {posts.map((post) => (
          <Col span={24} key={post.id}>
            <video src={post.url} controls={true} className="video-block" />
          </Col>
        ))}
      </Row>
    );
  };

  const showPost = (type) => {
    setActiveTab(type);
    // Refetch immediately. This used to wait three seconds for Elasticsearch to
    // refresh; the write path now uses refresh=wait_for, so the new post is
    // searchable by the time the upload response arrives.
    setSearchOption({ type: SEARCH_KEY.all, keyword: "" });
  };

  const operations = <CreatePostButton onShowPost={showPost} />;

  return (
    <div className="home">
      <SearchBar handleSearch={handleSearch} />
      <div className="display">
        <Tabs
          onChange={(key) => setActiveTab(key)}
          defaultActiveKey="image"
          activeKey={activeTab}
          tabBarExtraContent={operations}
        >
          <TabPane tab="Images" key="image">
            {activeTab === "image" && renderImages()}
          </TabPane>
          <TabPane tab="Videos" key="video">
            {activeTab === "video" && renderVideos()}
          </TabPane>
        </Tabs>
      </div>
    </div>
  );
}

export default Collection;
