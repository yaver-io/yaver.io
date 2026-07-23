# Todo Web — Yaver Serverless

Next.js Todo app backed by the Yaver Serverless Lite data API.

```bash
cd demo/yaver-serverless-todos/apps/web-next
npm install
NEXT_PUBLIC_YAVER_SERVERLESS_URL=http://127.0.0.1:18080 \
NEXT_PUBLIC_YAVER_SERVERLESS_SLUG=yaver-serverless-todo \
NEXT_PUBLIC_YAVER_SERVERLESS_TOKEN=pp_placeholder \
npm run dev
```

This demo has no Convex dependency. It uses:

```text
/data/{slug}/todos
```
