# ai-query：docs/08 §4.3 FastAPI 服务
# docker build -f deploy/docker/ai.Dockerfile -t hqpush/ai-query:0.1.0 .
FROM python:3.12-slim
WORKDIR /app
COPY ai-query/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ai-query/app ./app
USER 10001
EXPOSE 23041
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "23041"]
