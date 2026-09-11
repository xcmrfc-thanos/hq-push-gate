package com.hqpush.biz.config;

import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.web.client.RestClient;

/** ClickHouse HTTP 只读客户端（/quote/kline，docs/04 §4）；开发凭据与 compose 对齐。 */
@Configuration
public class ClickHouseConfig {

    @Bean
    public RestClient ckRestClient(
            @Value("${hq.clickhouse.url}") String baseUrl,
            @Value("${hq.clickhouse.user}") String user,
            @Value("${hq.clickhouse.password}") String password) {
        return RestClient.builder()
                .baseUrl(baseUrl)
                .defaultHeaders(h -> h.setBasicAuth(user, password))
                .build();
    }

    /** hq-gateway 内部端点客户端（模拟源调压代理）。 */
    @Bean
    public RestClient gatewayRestClient(
            @Value("${hq.gateway.url}") String baseUrl) {
        return RestClient.builder().baseUrl(baseUrl).build();
    }
}
