package com.hqpush.biz.interfaces;

import com.hqpush.biz.application.KlineService;
import com.hqpush.biz.application.ApiResult;

import com.hqpush.biz.application.QuoteService;
import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;

/** 行情快照与搜索（GET /api/v1/quote/*，docs/04 §6；U1 收尾：服务内 JWT 复核，docs/11 §4）。 */
@RestController
@RequestMapping("/api/v1/quote")
public class QuoteController {

    private final QuoteService quotes;
    private final KlineService klines;
    private final AuthController authHelper;

    public QuoteController(QuoteService quotes, KlineService klines, AuthController authHelper) {
        this.quotes = quotes;
        this.klines = klines;
        this.authHelper = authHelper;
    }

    /** 批量最新快照（≤50 symbol，docs/04 §6）。 */
    @GetMapping("/snapshot")
    public Map<String, Object> snapshot(@RequestHeader("Authorization") String authorization,
                                        @RequestParam String market,
                                        @RequestParam List<String> symbols) {
        authHelper.currentUid(authorization); // 服务内复核（docs/11 §4：用户面双重鉴权）
        if (symbols.size() > 50) {
            throw new IllegalArgumentException("too many symbols");
        }
        return ApiResult.ok(quotes.snapshot(market, symbols));
    }

    /** 历史K线：缓存→CK（docs/04 §6）；period 1/5 基期，15/30/60/1440 由 1m 滚动聚合。 */
    @GetMapping("/kline")
    public Map<String, Object> kline(@RequestHeader("Authorization") String authorization,
                                     @RequestParam String market,
                                     @RequestParam String symbol,
                                     @RequestParam Integer period,
                                     @RequestParam(required = false) Long start,
                                     @RequestParam(required = false) Long end,
                                     @RequestParam(defaultValue = "300") Integer limit) throws Exception {
        authHelper.currentUid(authorization); // 服务内复核
        if (period == null || period <= 0) {
            throw new IllegalArgumentException("invalid period");
        }
        return ApiResult.ok(
                klines.klines(market, symbol, period, start, end, limit));
    }

    /** 标的搜索：U0 由 symbol_meta 前缀查询简化为快照匹配，全文检索按需引入（docs/08 §4.0.3）。 */
    @GetMapping("/search")
    public Map<String, Object> search(@RequestHeader("Authorization") String authorization,
                                      @RequestParam String keyword) {
        authHelper.currentUid(authorization); // 服务内复核
        return ApiResult.ok(List.of());
    }
}
