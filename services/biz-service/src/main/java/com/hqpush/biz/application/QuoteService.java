package com.hqpush.biz.application;

import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Service;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/** 行情快照查询（GET /quote/snapshot）：Redis 热快照，quote-push 写入（docs/04 §5）。 */
@Service
public class QuoteService {

    private final StringRedisTemplate redis;

    public QuoteService(StringRedisTemplate redis) {
        this.redis = redis;
    }

    public List<Map<Object, Object>> snapshot(String market, List<String> symbols) {
        List<Map<Object, Object>> out = new ArrayList<>(symbols.size());
        for (String s : symbols) {
            Map<Object, Object> snap = redis.opsForHash().entries("snap:" + market + ":" + s);
            if (!snap.isEmpty()) {
                out.add(snap);
            }
        }
        return out;
    }
}
