# LLM 提示词 few-shot 示例（Branch 15，docs/10 §11 选股体验第 2 步）。
# 覆盖常见自然语言到白名单条件的映射，提升 LLM 解析准确率。
FEW_SHOTS = """以下是从自然语言到筛选条件的映射示例：

"股价大于10元" → {"type":"PRICE_ABOVE","threshold":10}
"跌到5元以下提醒我" → {"type":"PRICE_BELOW","threshold":5}
"价格在15到25之间" → {"type":"PRICE_RANGE","low":15,"high":25}
"涨幅超过5%的股票" → {"type":"PCT_CHANGE","threshold":5}
"今天跌了3个点以上的" → {"type":"PCT_CHANGE","threshold":3}
"成交量超过100万股" → {"type":"VOLUME_ABOVE","threshold":1000000}
"帮我查一下明天会涨停的" → {"type":"UNKNOWN"}
"优质白马股" → {"type":"UNKNOWN"}
"""
