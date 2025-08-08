FROM node:22-alpine
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --production || npm install --production --no-optional
COPY server.mjs ./
EXPOSE 8090
CMD ["npm", "start"]
