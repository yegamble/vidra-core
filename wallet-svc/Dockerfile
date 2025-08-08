# Build the NodeJS wallet microservice.  This simple Dockerfile installs
# dependencies and runs the service using Node 18 on Alpine for a small
# runtime image.

FROM node:18-alpine as deps
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --omit=dev

FROM node:18-alpine
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
EXPOSE 8090
CMD ["node", "server.mjs"]