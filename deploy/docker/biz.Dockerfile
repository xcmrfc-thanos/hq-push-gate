# biz-service：宿主机 mvn package 后 COPY（构建上下文 = 仓库根）
# docker build -f deploy/docker/biz.Dockerfile -t hqpush/biz-service:0.1.0 .
FROM eclipse-temurin:21-jre
WORKDIR /app
COPY services/biz-service/target/biz-service-1.0.0.jar app.jar
USER 10001
EXPOSE 23026
ENTRYPOINT ["java", "-jar", "app.jar"]
