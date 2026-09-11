// 规则条件校验器用例（ADR-042）：消费 tests/fixtures/conditions.v2.json（单一事实源，与 pytest 同源）。
// 路径口径：surefire 工作目录 = 模块根（services/biz-service），fixture 在仓库根 tests/ 下。
package com.hqpush.biz.application;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Iterator;

import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class RuleConditionValidatorTest {

    private static final ObjectMapper OM = new ObjectMapper();

    private static JsonNode fixture() throws Exception {
        Path p = Path.of("..", "..", "tests", "fixtures", "conditions.v2.json");
        return OM.readTree(Files.readAllBytes(p));
    }

    /** 数值跨型相等（IntNode 5 与 DoubleNode 5.0 视为相等），其余结构递归比较。 */
    private static boolean jsonEquals(JsonNode a, JsonNode b) {
        if (a.isNumber() && b.isNumber()) {
            return a.doubleValue() == b.doubleValue();
        }
        if (a.isObject() && b.isObject()) {
            if (a.size() != b.size()) {
                return false;
            }
            Iterator<String> it = a.fieldNames();
            while (it.hasNext()) {
                String f = it.next();
                if (!b.has(f) || !jsonEquals(a.get(f), b.get(f))) {
                    return false;
                }
            }
            return true;
        }
        if (a.isArray() && b.isArray()) {
            if (a.size() != b.size()) {
                return false;
            }
            for (int i = 0; i < a.size(); i++) {
                if (!jsonEquals(a.get(i), b.get(i))) {
                    return false;
                }
            }
            return true;
        }
        return a.equals(b);
    }

    @Test
    void validRuleCasesProduceCanonical() throws Exception {
        for (JsonNode c : fixture().path("valid_rule")) {
            String out = RuleConditionValidator.canonicalize(
                    c.path("ruleType").asText(), OM.writeValueAsString(c.path("input")));
            assertTrue(jsonEquals(OM.readTree(out), c.path("canonical")),
                    "canonical mismatch: " + c.path("name").asText() + " -> " + out);
        }
    }

    @Test
    void invalidRuleCasesRejected() throws Exception {
        for (JsonNode c : fixture().path("invalid_rule")) {
            assertThrows(IllegalArgumentException.class,
                    () -> RuleConditionValidator.canonicalize(
                            c.path("ruleType").asText(), OM.writeValueAsString(c.path("input"))),
                    "must reject: " + c.path("name").asText());
        }
    }
}
