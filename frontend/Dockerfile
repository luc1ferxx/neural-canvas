FROM node:22-alpine AS build

WORKDIR /app

# npm ci, not npm install: it installs exactly what package-lock.json pins and
# fails if the two disagree, so an image build cannot quietly resolve a different
# dependency tree than the one that was tested.
COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# The API base URL is a build argument, not a runtime variable, because Create
# React App inlines REACT_APP_* values into the bundle at build time. There is no
# way to change it afterwards without rebuilding -- and no point trying, since
# whatever is in the bundle is readable by anyone who loads the page. Only
# non-secret values belong here.
#
# Its value is the URL the *browser* uses, which is not the same as the one the
# frontend container could reach: in compose the backend answers on
# http://backend:8080 inside the network, but that name means nothing to a browser
# on the host.
ARG REACT_APP_API_BASE_URL=http://localhost:8080
ENV REACT_APP_API_BASE_URL=$REACT_APP_API_BASE_URL

RUN npm run build

# Runtime stage. The build output is static files, so nothing from the Node stage
# needs to ship -- no node_modules, no toolchain, no source.
FROM nginx:1.30-alpine

COPY --from=build /app/build /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 3000
