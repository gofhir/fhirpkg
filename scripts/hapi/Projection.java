import ca.uhn.fhir.context.FhirContext;
import ca.uhn.fhir.parser.IParser;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

// Arnés del oráculo HAPI (plan 005, D7): lee jobs.tsv («entrada, modo, elementos,
// salida» separados por tabulador), proyecta cada recurso con el parser de HAPI
// como lo hace RestfulServerUtils y escribe la salida (o «ERROR: ...»).
// Uso: java -cp structures.jar:validator_cli.jar Projection R4|R4B|R5 jobs.tsv
public class Projection {
  public static void main(String[] a) throws Exception {
    FhirContext ctx = switch (a[0]) {
      case "R4" -> FhirContext.forR4();
      case "R4B" -> FhirContext.forR4B();
      default -> FhirContext.forR5();
    };
    for (String line : Files.readAllLines(Path.of(a[1]), StandardCharsets.UTF_8)) {
      String[] f = line.split("\t", -1);
      try {
        IParser p = ctx.newJsonParser();
        p.setPrettyPrint(false);
        var res = p.parseResource(Files.readString(Path.of(f[0]), StandardCharsets.UTF_8));
        String type = res.fhirType();
        switch (f[1]) {
          case "true" -> p.setSummaryMode(true);
          case "data" -> p.setSuppressNarratives(true);
          case "text" -> p.setEncodeElements(new HashSet<>(List.of(type + ".text", type + ".id", type + ".meta", type + ".(mandatory)")));
          case "elements" -> {
            Set<String> s = new HashSet<>();
            for (String n : f[2].split(",")) s.add(type + "." + n);
            p.setEncodeElements(s);
          }
          default -> {}
        }
        Files.writeString(Path.of(f[3]), p.encodeResourceToString(res), StandardCharsets.UTF_8);
      } catch (Throwable t) {
        Files.writeString(Path.of(f[3]), "ERROR: " + t.getClass().getName(), StandardCharsets.UTF_8);
      }
    }
  }
}
