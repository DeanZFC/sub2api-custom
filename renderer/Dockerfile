FROM mcr.microsoft.com/playwright:v1.56.1-noble

WORKDIR /opt/quality-renderer
COPY renderer/package*.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force
COPY renderer/server.mjs renderer/integration.test.mjs ./
RUN mkdir -p /review-renderer && chown pwuser:pwuser /review-renderer
USER pwuser
ENV RENDERER_SOCKET=/review-renderer/renderer.sock
CMD ["node", "server.mjs"]
