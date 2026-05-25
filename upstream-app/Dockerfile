FROM oven/bun:1 AS runtime

WORKDIR /app
COPY app.js /app/app.js

EXPOSE 3000

CMD ["bun", "app.js"]
