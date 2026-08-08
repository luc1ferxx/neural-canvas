import { useState } from "react";

import PhotoAlbum from "react-photo-album";
import Lightbox from "yet-another-react-lightbox";

import Fullscreen from "yet-another-react-lightbox/plugins/fullscreen";
import Slideshow from "yet-another-react-lightbox/plugins/slideshow";
import Thumbnails from "yet-another-react-lightbox/plugins/thumbnails";
import Zoom from "yet-another-react-lightbox/plugins/zoom";
import styled from "styled-components";
import Typography from "@mui/material/Typography";
import Paper from "@mui/material/Paper";
import InputBase from "@mui/material/InputBase";
import IconButton from "@mui/material/IconButton";
import ArrowForwardIosIcon from "@mui/icons-material/ArrowForward";
import api from "../api";
import { message } from "antd";
import { CircularProgress } from "@mui/material";

const Overlay = styled.div`
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 1000;
`;

const MainContainer = styled.div`
  background-color: #27272a;
  height: 100%;
  min-height: 100vh;
`;

const HeaderContainer = styled.div`
  display: flex;
  flex-direction: column;
  align-items: center;
`;

export default function Landing() {
  const [index, setIndex] = useState(-1);
  const [inputValue, setInputValue] = useState("");
  const [isGeneratingImage, setIsGeneratingImage] = useState(false);
  const [photos, setPhotos] = useState([]);

  // Generation runs entirely on the backend: it calls DALL-E, stores the result
  // in GCS and indexes the post, then returns it. The OpenAI key stays on the
  // server -- it used to be read from process.env here, which Create React App
  // inlines into the production bundle for every visitor to read.
  const createImage = async () => {
    const prompt = inputValue.trim();
    if (!prompt) {
      message.warning("Please describe the image you want to generate.");
      return;
    }

    try {
      setIsGeneratingImage(true);

      const res = await api.post("/generate", { prompt });

      const post = res.data;
      setPhotos([{ src: post.url, width: 200, height: 200 }]);
      message.success("Image generated and saved to your collection!");
    } catch (error) {
      message.error(
        "Something went wrong with AI generated image. Please try again.",
      );
      console.log("AI generated image failed: ", error.message);
    } finally {
      setIsGeneratingImage(false);
    }
  };

  const handleInputChange = (event) => {
    setInputValue(event.target.value);
  };

  return (
    <MainContainer>
      {isGeneratingImage && (
        <Overlay>
          <CircularProgress color="info" size={80} />
        </Overlay>
      )}

      <HeaderContainer>
        <Typography
          variant="h1"
          marginTop="128px"
          noWrap
          component="div"
          sx={{
            fontFamily: "Roboto",
            // Was frontSize="5.25rem": a typo, so the size never applied and
            // React rejected it as an unknown DOM attribute.
            fontSize: "5.25rem",
            color: "white",
            textDecoration: "none",
          }}
        >
          Social AI
        </Typography>
        <Typography
          variant="h5"
          component="div"
          sx={{
            mr: 2,
            fontFamily: "Roboto",
            fontSize: "1.2rem",
            color: "white",
            textDecoration: "none",
            margin: " 0 20px",
            textAlign: "center",
          }}
        >
          Unleash Creativity, Share Memories-Where AI meets Your Imagination!
        </Typography>
        <Paper
          component="form"
          sx={{
            p: "2px 4px",
            display: "flex",
            alignItems: "center",
            width: "80%",
            maxWidth: "600px",
            borderRadius: "10px",
            marginTop: "32px",
            marginBottom: "64px",
          }}
        >
          <InputBase
            multiline
            sx={{ ml: 1, flex: 1 }}
            placeholder="Enter a detailed description of the photo you want to generate..."
            inputProps={{ "aria-label": "search" }}
            value={inputValue}
            onChange={handleInputChange}
          />
          <IconButton
            type="button"
            sx={{ p: "10px" }}
            aria-label="search"
            onClick={() => {
              createImage();
            }}
          >
            <ArrowForwardIosIcon />
          </IconButton>
        </Paper>
      </HeaderContainer>
      <PhotoAlbum
        photos={photos}
        layout="rows"
        targetRowHeight={200}
        onClick={({ index }) => setIndex(index)}
      />
      <Lightbox
        slides={photos}
        open={index >= 0}
        index={index}
        close={() => setIndex(-1)}
        plugins={[Fullscreen, Slideshow, Thumbnails, Zoom]}
      />
    </MainContainer>
  );
}
