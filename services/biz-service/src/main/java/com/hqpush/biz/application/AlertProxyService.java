package com.hqpush.biz.application;

import com.hqpush.biz.interfaces.TraceFilter;
import org.slf4j.MDC;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Service;
import org.springframework.web.client.RestClient;

import java.util.Map;

/**
 * 告警补拉与 ACK：代理 notify 内部 API（docs/07 §6 投递流程）。
 * 服务端保证 72h 保留窗口内不丢，客户端经 cursor 补拉并按 delivery_id 去重。
 * trace_id 由 TraceFilter 写入 MDC，此处透传给 notify。
 */
@Service
public class AlertProxyService {

    private final RestClient notify;

    public AlertProxyService(@Value("${hq.notify.url:http://localhost:8083}") String notifyUrl,
                             @Value("${hq.notify.token:dev-internal-token}") String token) {
        this.notify = RestClient.builder()
                .baseUrl(notifyUrl)
                .defaultHeader("X-Internal-Token", token)
                .requestInterceptor((req, body, exec) -> {
                    String traceId = MDC.get(TraceFilter.MDC_KEY);
                    if (traceId != null && !traceId.isBlank()) {
                        req.getHeaders().set(TraceFilter.HEADER, traceId);
                    }
                    return exec.execute(req, body);
                })
                .build();
    }

    @SuppressWarnings("unchecked")
    public Map<String, Object> pull(long userId, long cursor, int limit) {
        return notify.get()
                .uri(b -> b.path("/internal/alerts")
                        .queryParam("user_id", userId)
                        .queryParam("cursor", cursor)
                        .queryParam("limit", limit)
                        .build())
                .retrieve()
                .body(Map.class);
    }

    @SuppressWarnings("unchecked")
    public boolean ack(long userId, String deliveryId) {
        Map<String, Object> resp = notify.post()
                .uri("/internal/alerts/ack")
                .header("Content-Type", "application/json")
                .body(Map.of("delivery_id", deliveryId, "user_id", userId))
                .retrieve()
                .body(Map.class);
        return resp != null && resp.getOrDefault("code", -1).equals(0);
    }
}
